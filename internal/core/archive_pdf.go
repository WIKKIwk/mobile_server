package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/url"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func (a *ERPAuthenticator) WerkaArchivePDF(ctx context.Context, principal Principal, kind WerkaArchiveKind, period WerkaArchivePeriod) (GeneratedFile, error) {
	report, err := a.WerkaArchive(ctx, kind, period)
	if err != nil {
		return GeneratedFile{}, err
	}
	reportID := buildArchiveReportID(kind)
	verifyCode, err := buildArchiveVerifyCode()
	if err != nil {
		return GeneratedFile{}, err
	}
	datasetHash, err := buildArchiveDatasetHash(report)
	if err != nil {
		return GeneratedFile{}, err
	}
	verifyURL := buildArchiveVerifyURL(reportID, verifyCode)
	if a.reportExports != nil {
		if err := a.reportExports.Put(ReportExportRecord{
			ReportID:        reportID,
			VerifyCode:      verifyCode,
			Kind:            kind,
			Period:          period,
			From:            report.From,
			To:              report.To,
			GeneratedAt:     time.Now().UTC(),
			GeneratedByRole: principal.Role,
			GeneratedByRef:  strings.TrimSpace(principal.Ref),
			GeneratedByName: strings.TrimSpace(principal.DisplayName),
			DatasetHash:     datasetHash,
			RecordCount:     report.Summary.RecordCount,
		}); err != nil {
			return GeneratedFile{}, err
		}
	}

	body, err := renderWerkaArchivePDF(principal, report, reportID, verifyCode, verifyURL)
	if err != nil {
		return GeneratedFile{}, err
	}
	return GeneratedFile{
		Filename:    fmt.Sprintf("werka-%s-%s-%s.pdf", kind, period, time.Now().Format("2006-01-02")),
		ContentType: "application/pdf",
		Body:        body,
		ReportID:    reportID,
		VerifyCode:  verifyCode,
		VerifyURL:   verifyURL,
	}, nil
}

func (a *ERPAuthenticator) VerifyArchiveReport(reportID, verifyCode string) (ReportVerifyResponse, error) {
	if a.reportExports == nil {
		return ReportVerifyResponse{Valid: false, Status: "not_configured"}, nil
	}
	record, err := a.reportExports.Get(strings.TrimSpace(reportID))
	if err != nil {
		return ReportVerifyResponse{}, err
	}
	if strings.TrimSpace(record.ReportID) == "" {
		return ReportVerifyResponse{Valid: false, Status: "not_found"}, nil
	}
	if strings.TrimSpace(record.VerifyCode) != strings.TrimSpace(verifyCode) {
		return ReportVerifyResponse{
			Valid:    false,
			Status:   "invalid_code",
			ReportID: record.ReportID,
		}, nil
	}
	return ReportVerifyResponse{
		Valid:           true,
		Status:          "valid",
		ReportID:        record.ReportID,
		VerifyCode:      record.VerifyCode,
		Kind:            record.Kind,
		Period:          record.Period,
		From:            record.From,
		To:              record.To,
		GeneratedAt:     record.GeneratedAt,
		GeneratedByRole: record.GeneratedByRole,
		GeneratedByRef:  record.GeneratedByRef,
		GeneratedByName: record.GeneratedByName,
		DatasetHash:     record.DatasetHash,
		RecordCount:     record.RecordCount,
	}, nil
}

func renderWerkaArchivePDF(principal Principal, report WerkaArchiveResponse, reportID, verifyCode, verifyURL string) ([]byte, error) {
	pages, err := renderArchivePages(principal, report, reportID, verifyCode, verifyURL)
	if err != nil {
		return nil, err
	}
	return buildRasterPDF(pages), nil
}

func buildArchiveDatasetHash(report WerkaArchiveResponse) (string, error) {
	payload, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func buildArchiveVerifyURL(reportID, verifyCode string) string {
	return "https://core.wspace.sbs/v1/mobile/werka/archive/pdf/verify?id=" +
		url.QueryEscape(strings.TrimSpace(reportID)) +
		"&code=" + url.QueryEscape(strings.TrimSpace(verifyCode))
}

type archiveTextStyle struct {
	face  font.Face
	color color.Color
}

type archiveFonts struct {
	title    font.Face
	subtitle font.Face
	header   font.Face
	body     font.Face
	bodyBold font.Face
	meta     font.Face
	footer   font.Face
}

type archiveColumn struct {
	label string
	x     int
	width int
	align string
}

type archiveRow struct {
	date   string
	docID  string
	party  string
	item   string
	qty    string
	status string
}

var corporateArchiveColumns = []archiveColumn{
	{label: "Sana", x: 48, width: 145, align: "left"},
	{label: "Hujjat", x: 193, width: 190, align: "left"},
	{label: "Counterparty", x: 383, width: 220, align: "left"},
	{label: "Mahsulot", x: 603, width: 355, align: "left"},
	{label: "Miqdor", x: 958, width: 108, align: "right"},
	{label: "Status", x: 1066, width: 126, align: "left"},
}

func renderArchivePages(principal Principal, report WerkaArchiveResponse, reportID, verifyCode, verifyURL string) ([]*image.RGBA, error) {
	const (
		pageWidth      = 1240
		pageHeight     = 1754
		headerHeight   = 150
		summaryHeight  = 74
		tableHeadH     = 40
		rowHeight      = 36
		footerHeight   = 28
		topMargin      = 44
		leftMargin     = 48
		contentWidth   = 1144
		rowSpacing     = 0
		tableBottomPad = 30
	)

	fonts, err := loadArchiveFonts()
	if err != nil {
		return nil, err
	}

	generatedBy := strings.TrimSpace(principal.DisplayName)
	if generatedBy == "" {
		generatedBy = strings.TrimSpace(principal.Ref)
	}
	if generatedBy == "" {
		generatedBy = "Werka"
	}

	rows := make([]archiveRow, 0, len(report.Items))
	for _, item := range report.Items {
		rows = append(rows, archiveRow{
			date:   formatArchiveDateForCell(item.CreatedLabel),
			docID:  item.ID,
			party:  strings.TrimSpace(item.SupplierName),
			item:   archiveProductName(item),
			qty:    fmt.Sprintf("%.2f %s", archiveQtyForKind(report.Kind, item), strings.TrimSpace(item.UOM)),
			status: archiveStatusLabel(item.Status),
		})
	}

	reportTitle := archiveReportTitle(report.Kind)
	periodTitle := archivePeriodTitle(report.Period)
	pages := make([]*image.RGBA, 0, 4)

	newPage := func() (*image.RGBA, int) {
		page := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
		draw.Draw(page, page.Bounds(), &image.Uniform{color.RGBA{248, 249, 251, 255}}, image.Point{}, draw.Src)
		y := topMargin

		y = drawArchiveHeader(page, fonts, leftMargin, y, contentWidth, headerHeight, reportTitle, periodTitle, generatedBy, report, reportID, verifyCode)
		y += 12
		y = drawArchiveSummary(page, fonts, leftMargin, y, contentWidth, summaryHeight, report.Summary)
		y += 12
		y = drawArchiveTableHeader(page, fonts, y, tableHeadH)
		return page, y
	}

	page, y := newPage()
	maxTableY := pageHeight - footerHeight - tableBottomPad
	for idx, row := range rows {
		if y+rowHeight > maxTableY {
			applyArchiveAntiOCR(page)
			drawArchiveFooter(page, fonts, leftMargin, pageHeight-footerHeight, contentWidth, len(pages)+1, reportID, verifyCode, verifyURL)
			pages = append(pages, page)
			page, y = newPage()
		}
		drawArchiveRow(page, fonts, row, y, rowHeight, idx%2 == 0)
		y += rowHeight + rowSpacing
	}
	applyArchiveAntiOCR(page)
	drawArchiveFooter(page, fonts, leftMargin, pageHeight-footerHeight, contentWidth, len(pages)+1, reportID, verifyCode, verifyURL)
	pages = append(pages, page)
	return pages, nil
}

func loadArchiveFonts() (archiveFonts, error) {
	regularTTF, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return archiveFonts{}, err
	}
	boldTTF, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return archiveFonts{}, err
	}

	makeFace := func(ttf *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(ttf, &opentype.FaceOptions{Size: size, DPI: 144, Hinting: font.HintingFull})
	}

	title, err := makeFace(regularTTF, 22)
	if err != nil {
		return archiveFonts{}, err
	}
	subtitle, err := makeFace(regularTTF, 11)
	if err != nil {
		return archiveFonts{}, err
	}
	header, err := makeFace(boldTTF, 9)
	if err != nil {
		return archiveFonts{}, err
	}
	body, err := makeFace(regularTTF, 7)
	if err != nil {
		return archiveFonts{}, err
	}
	bodyBold, err := makeFace(boldTTF, 7)
	if err != nil {
		return archiveFonts{}, err
	}
	meta, err := makeFace(regularTTF, 6)
	if err != nil {
		return archiveFonts{}, err
	}
	footer, err := makeFace(regularTTF, 5.5)
	if err != nil {
		return archiveFonts{}, err
	}
	return archiveFonts{
		title:    title,
		subtitle: subtitle,
		header:   header,
		body:     body,
		bodyBold: bodyBold,
		meta:     meta,
		footer:   footer,
	}, nil
}

func drawArchiveHeader(page *image.RGBA, fonts archiveFonts, x, y, width, height int, title, period, generatedBy string, report WerkaArchiveResponse, reportID, verifyCode string) int {
	fillRect(page, x, y, width, height, color.RGBA{40, 54, 78, 255})
	fillRect(page, x, y, width, 5, color.RGBA{38, 166, 154, 255})

	light := color.RGBA{248, 250, 252, 255}
	muted := color.RGBA{205, 214, 227, 255}
	dark := color.RGBA{44, 56, 76, 255}

	drawText(page, archiveTextStyle{face: fonts.header, color: muted}, x+18, y+24, "ACCORD ARCHIVE REPORT")
	drawText(page, archiveTextStyle{face: fonts.title, color: light}, x+18, y+60, title)
	drawText(page, archiveTextStyle{face: fonts.subtitle, color: light}, x+18, y+86, "Period: "+period)
	drawText(page, archiveTextStyle{face: fonts.subtitle, color: light}, x+18, y+108, "Oraliq: "+report.From.Format("2006-01-02 15:04")+" -> "+report.To.Format("2006-01-02 15:04"))
	drawText(page, archiveTextStyle{face: fonts.subtitle, color: light}, x+18, y+130, "Generated by: "+generatedBy)

	panelX := x + width - 290
	fillRect(page, panelX, y+16, 272, 116, color.RGBA{248, 250, 252, 255})
	strokeRect(page, panelX, y+16, 272, 116, color.RGBA{197, 206, 219, 255})
	drawText(page, archiveTextStyle{face: fonts.header, color: dark}, panelX+14, y+38, "Compliance Panel")
	drawText(page, archiveTextStyle{face: fonts.meta, color: dark}, panelX+14, y+58, "Mode: Flattened static copy")
	drawText(page, archiveTextStyle{face: fonts.meta, color: dark}, panelX+14, y+76, fitStringToWidth(&font.Drawer{Face: fonts.meta}, "Report ID: "+reportID, 244))
	drawText(page, archiveTextStyle{face: fonts.meta, color: dark}, panelX+14, y+94, fitStringToWidth(&font.Drawer{Face: fonts.meta}, "Verify code: "+verifyCode, 244))

	return y + height
}

func drawArchiveSummary(page *image.RGBA, fonts archiveFonts, x, y, width, height int, summary WerkaArchiveSummary) int {
	fillRect(page, x, y, width, height, color.RGBA{241, 244, 248, 255})
	strokeRect(page, x, y, width, height, color.RGBA{221, 227, 235, 255})

	boxX := x + 18
	drawSummaryMetric(page, fonts, boxX, y+14, 190, 44, "Yozuvlar soni", fmt.Sprintf("%d", summary.RecordCount))
	boxX += 208
	for _, total := range summary.TotalsByUOM {
		drawSummaryMetric(page, fonts, boxX, y+14, 150, 44, strings.TrimSpace(total.UOM), fmt.Sprintf("%.2f", total.Qty))
		boxX += 168
	}
	return y + height
}

func drawSummaryMetric(page *image.RGBA, fonts archiveFonts, x, y, width, height int, label, value string) {
	fillRect(page, x, y, width, height, color.RGBA{255, 255, 255, 255})
	strokeRect(page, x, y, width, height, color.RGBA{216, 223, 233, 255})
	drawText(page, archiveTextStyle{face: fonts.meta, color: color.RGBA{90, 103, 122, 255}}, x+12, y+17, label)
	drawText(page, archiveTextStyle{face: fonts.bodyBold, color: color.RGBA{32, 43, 59, 255}}, x+12, y+33, value)
}

func drawArchiveTableHeader(page *image.RGBA, fonts archiveFonts, y, height int) int {
	for _, col := range corporateArchiveColumns {
		fillRect(page, col.x, y, col.width, height, color.RGBA{53, 67, 89, 255})
		strokeRect(page, col.x, y, col.width, height, color.RGBA{112, 129, 154, 255})
		drawCellText(page, archiveTextStyle{face: fonts.header, color: color.White}, col, y, height, col.label)
	}
	return y + height
}

func drawArchiveRow(page *image.RGBA, fonts archiveFonts, row archiveRow, y, height int, zebra bool) {
	bg := color.RGBA{255, 255, 255, 255}
	if zebra {
		bg = color.RGBA{244, 247, 251, 255}
	}
	grid := color.RGBA{220, 226, 234, 255}
	statusBg := color.RGBA{255, 244, 204, 255}
	bodyStyle := archiveTextStyle{face: fonts.body, color: color.RGBA{39, 48, 62, 255}}
	statusStyle := archiveTextStyle{face: fonts.body, color: color.RGBA{164, 110, 0, 255}}

	for _, col := range corporateArchiveColumns {
		fill := color.Color(bg)
		if col.label == "Status" {
			fill = statusBg
		}
		fillRect(page, col.x, y, col.width, height, fill)
		strokeRect(page, col.x, y, col.width, height, grid)
	}

	values := []struct {
		col   archiveColumn
		text  string
		style archiveTextStyle
	}{
		{corporateArchiveColumns[0], row.date, bodyStyle},
		{corporateArchiveColumns[1], row.docID, bodyStyle},
		{corporateArchiveColumns[2], row.party, bodyStyle},
		{corporateArchiveColumns[3], row.item, bodyStyle},
		{corporateArchiveColumns[4], row.qty, bodyStyle},
		{corporateArchiveColumns[5], row.status, statusStyle},
	}
	for _, value := range values {
		drawCellText(page, value.style, value.col, y, height, value.text)
	}
}

func drawArchiveFooter(page *image.RGBA, fonts archiveFonts, x, y, width, pageNumber int, reportID, verifyCode, verifyURL string) {
	fillRect(page, x, y, width, 24, color.RGBA{235, 239, 244, 255})
	strokeRect(page, x, y, width, 24, color.RGBA{216, 223, 233, 255})

	left := fmt.Sprintf("Page %d", pageNumber)
	right := fmt.Sprintf("Flattened static copy • %s • %s", strings.TrimSpace(reportID), strings.TrimSpace(verifyCode))
	drawText(page, archiveTextStyle{face: fonts.footer, color: color.RGBA{72, 82, 98, 255}}, x+10, y+16, left)
	drawText(page, archiveTextStyle{face: fonts.footer, color: color.RGBA{72, 82, 98, 255}}, x+260, y+16, fitStringToWidth(&font.Drawer{Face: fonts.footer}, right, 600))
	drawText(page, archiveTextStyle{face: fonts.footer, color: color.RGBA{108, 118, 132, 255}}, x+890, y+16, fitStringToWidth(&font.Drawer{Face: fonts.footer}, verifyURL, 290))
}

func drawCellText(page *image.RGBA, style archiveTextStyle, col archiveColumn, y, height int, text string) {
	drawer := &font.Drawer{Face: style.face}
	fitted := fitStringToWidth(drawer, text, col.width-16)
	textWidth := drawer.MeasureString(fitted).Ceil()
	textX := col.x + 8
	if col.align == "right" {
		textX = col.x + col.width - 8 - textWidth
	}
	textY := y + (height / 2) + 3
	drawText(page, style, textX, textY, fitted)
}

func drawText(img *image.RGBA, style archiveTextStyle, x, y int, text string) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(style.color),
		Face: style.face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func fitStringToWidth(drawer *font.Drawer, value string, maxWidth int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if drawer.MeasureString(trimmed).Ceil() <= maxWidth {
		return trimmed
	}
	runes := []rune(trimmed)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + "…"
		if drawer.MeasureString(candidate).Ceil() <= maxWidth {
			return candidate
		}
	}
	return "…"
}

func archiveProductName(item DispatchRecord) string {
	itemName := strings.TrimSpace(item.ItemName)
	itemCode := strings.TrimSpace(item.ItemCode)
	if itemName == "" {
		return itemCode
	}
	if itemCode == "" || strings.EqualFold(itemName, itemCode) {
		return itemName
	}
	return itemName
}

func formatArchiveDateForCell(value string) string {
	trimmed := strings.TrimSpace(value)
	if idx := strings.Index(trimmed, " "); idx > 0 {
		datePart := trimmed[:idx]
		timePart := strings.TrimSpace(trimmed[idx+1:])
		if dot := strings.Index(timePart, "."); dot > 0 {
			timePart = timePart[:dot]
		}
		return datePart + " " + timePart
	}
	return trimmed
}

func archiveStatusLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	return strings.Title(strings.ToLower(trimmed))
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
}

func strokeRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	fillRect(img, x, y, w, 1, c)
	fillRect(img, x, y+h-1, w, 1, c)
	fillRect(img, x, y, 1, h, c)
	fillRect(img, x+w-1, y, 1, h, c)
}

func buildRasterPDF(pages []*image.RGBA) []byte {
	maxID := 4 + len(pages)*3
	objects := make([]string, maxID+1)
	objects[1] = "<< /Type /Catalog /Pages 2 0 R >>"
	kids := make([]string, 0, len(pages))

	for pageIndex, page := range pages {
		pageID := 5 + pageIndex*3
		imageID := pageID + 1
		contentID := pageID + 2
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
		imageObject := buildImageObject(page)
		content := fmt.Sprintf("q %d 0 0 %d 0 0 cm /Im0 Do Q", page.Bounds().Dx(), page.Bounds().Dy())
		objects[pageID] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>", page.Bounds().Dx(), page.Bounds().Dy(), imageID, contentID)
		objects[imageID] = imageObject
		objects[contentID] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	}

	objects[2] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), strings.Join(kids, " "))
	objects[3] = "<< >>"
	objects[4] = "<< >>"

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xFF\xFF\xFF\xFF\n")
	offsets := make([]int, maxID+1)
	for id := 1; id <= maxID; id++ {
		offsets[id] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", id, objects[id])
	}
	xrefOffset := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", maxID+1)
	out.WriteString("0000000000 65535 f \n")
	for id := 1; id <= maxID; id++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&out, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", maxID+1, xrefOffset)
	return out.Bytes()
}

func buildImageObject(img *image.RGBA) string {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	var encoded bytes.Buffer
	_ = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 82})
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream", w, h, encoded.Len(), encoded.String())
}

func buildArchiveReportID(kind WerkaArchiveKind) string {
	code, _ := buildArchiveVerifyCode()
	suffix := strings.ReplaceAll(code, "-", "")
	if len(suffix) > 4 {
		suffix = suffix[:4]
	}
	return fmt.Sprintf("WAR-%s-%s-%s", strings.ToUpper(string(kind)), time.Now().Format("20060102-150405"), suffix)
}

func buildArchiveVerifyCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "=")
	token = strings.ToUpper(token)
	if len(token) < 12 {
		return token, nil
	}
	return fmt.Sprintf("%s-%s-%s", token[:4], token[4:8], token[8:12]), nil
}

func archiveReportTitle(kind WerkaArchiveKind) string {
	switch kind {
	case WerkaArchiveKindReceived:
		return "Werka Qabul Qilingan Hisoboti"
	case WerkaArchiveKindReturned:
		return "Werka Qaytarilgan Hisoboti"
	default:
		return "Werka Jo'natilgan Hisoboti"
	}
}

func archivePeriodTitle(period WerkaArchivePeriod) string {
	switch period {
	case WerkaArchivePeriodDaily:
		return "Kunlik"
	case WerkaArchivePeriodMonthly:
		return "Oylik"
	default:
		return "Yillik"
	}
}

func applyArchiveAntiOCR(img *image.RGBA) {
	bounds := img.Bounds()
	scaledW := int(float64(bounds.Dx()) * 0.90)
	scaledH := int(float64(bounds.Dy()) * 0.90)
	if scaledW > 0 && scaledH > 0 {
		reduced := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
		xdraw.ApproxBiLinear.Scale(reduced, reduced.Bounds(), img, bounds, draw.Src, nil)
		xdraw.ApproxBiLinear.Scale(img, bounds, reduced, reduced.Bounds(), draw.Src, nil)
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			offset := img.PixOffset(x, y)
			r := img.Pix[offset]
			g := img.Pix[offset+1]
			b := img.Pix[offset+2]
			luma := (int(r)*299 + int(g)*587 + int(b)*114) / 1000
			if luma < 236 {
				wave := ((x*13 + y*7) % 17) - 8
				img.Pix[offset] = clamp8(int(r) + wave)
				img.Pix[offset+1] = clamp8(int(g) - wave/2)
				img.Pix[offset+2] = clamp8(int(b) + wave/3)
			}
			if luma < 210 && (x+y)%3 == 0 {
				img.Pix[offset] = mixChannel(img.Pix[offset], 247, 26)
				img.Pix[offset+1] = mixChannel(img.Pix[offset+1], 244, 18)
				img.Pix[offset+2] = mixChannel(img.Pix[offset+2], 238, 10)
			}
			if luma < 170 && x%5 == 0 {
				img.Pix[offset] = clamp8(int(img.Pix[offset]) + 22)
				img.Pix[offset+1] = clamp8(int(img.Pix[offset+1]) - 8)
				img.Pix[offset+2] = clamp8(int(img.Pix[offset+2]) + 6)
			}
			if y%4 == 0 {
				img.Pix[offset] = mixChannel(img.Pix[offset], 244, 6)
				img.Pix[offset+1] = mixChannel(img.Pix[offset+1], 243, 6)
				img.Pix[offset+2] = mixChannel(img.Pix[offset+2], 239, 6)
			}
		}
	}
}

func mixChannel(base uint8, overlay uint8, alpha uint8) uint8 {
	keep := int(255 - alpha)
	return uint8((int(base)*keep + int(overlay)*int(alpha)) / 255)
}

func clamp8(value int) uint8 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}
