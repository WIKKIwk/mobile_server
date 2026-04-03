package core

import (
	"bytes"
	"compress/zlib"
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
	"net/url"
	"strings"
	"time"

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
		return ReportVerifyResponse{
			Valid:  false,
			Status: "not_found",
		}, nil
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

type textStyle struct {
	face  font.Face
	color color.Color
}

type tableRow struct {
	date     string
	docID    string
	party    string
	item     string
	qty      string
	status   string
	itemName string
}

type fontPack struct {
	title    font.Face
	subtitle font.Face
	body     font.Face
	small    font.Face
	bold     font.Face
}

func renderArchivePages(principal Principal, report WerkaArchiveResponse, reportID, verifyCode, verifyURL string) ([]*image.RGBA, error) {
	const (
		pageWidth  = 1240
		pageHeight = 1754
	)

	fonts, err := loadArchiveFonts()
	if err != nil {
		return nil, err
	}

	reportTitle := archiveReportTitle(report.Kind)
	periodTitle := archivePeriodTitle(report.Period)
	generatedBy := strings.TrimSpace(principal.DisplayName)
	if generatedBy == "" {
		generatedBy = strings.TrimSpace(principal.Ref)
	}
	if generatedBy == "" {
		generatedBy = "Werka"
	}

	rows := make([]tableRow, 0, len(report.Items))
	for _, item := range report.Items {
		rows = append(rows, tableRow{
			date:     item.CreatedLabel,
			docID:    item.ID,
			party:    item.SupplierName,
			item:     item.ItemCode,
			qty:      fmt.Sprintf("%.2f %s", archiveQtyForKind(report.Kind, item), item.UOM),
			status:   string(item.Status),
			itemName: item.ItemName,
		})
	}

	pages := make([]*image.RGBA, 0, 4)
	page, y := newArchivePage(pageWidth, pageHeight)
	drawArchiveWatermark(page, fonts)
	y = drawArchiveHeader(page, fonts, reportTitle, periodTitle, generatedBy, report, reportID, verifyCode, verifyURL, y)
	y = drawArchiveSummary(page, fonts, report.Summary, y)
	y = drawArchiveTableHeader(page, fonts, y)
	for _, row := range rows {
		nextY := y - archiveRowHeight(row)
		if nextY < 90 {
			drawArchiveFooter(page, fonts, len(pages)+1)
			pages = append(pages, page)
			page, y = newArchivePage(pageWidth, pageHeight)
			drawArchiveWatermark(page, fonts)
			y = drawArchiveTableHeader(page, fonts, y)
			nextY = y - archiveRowHeight(row)
		}
		drawArchiveRow(page, fonts, row, y)
		y = nextY
	}
	drawArchiveFooter(page, fonts, len(pages)+1)
	pages = append(pages, page)
	return pages, nil
}

func loadArchiveFonts() (fontPack, error) {
	regularTTF, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return fontPack{}, err
	}
	boldTTF, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return fontPack{}, err
	}
	title, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 30, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	subtitle, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 24, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	body, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 16, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	small, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 12, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	bold, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 16, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	return fontPack{
		title:    title,
		subtitle: subtitle,
		body:     body,
		small:    small,
		bold:     bold,
	}, nil
}

func newArchivePage(pageWidth, pageHeight int) (*image.RGBA, int) {
	page := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
	draw.Draw(page, page.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
	return page, 1660
}

func drawArchiveWatermark(page *image.RGBA, fonts fontPack) {
	watermarkStyle := textStyle{
		face:  fonts.subtitle,
		color: color.RGBA{225, 225, 225, 255},
	}
	for y := 260; y <= 1500; y += 260 {
		drawText(page, watermarkStyle, 260, y, "ACCORD ARCHIVE")
	}
}

func drawArchiveHeader(page *image.RGBA, fonts fontPack, reportTitle, periodTitle, generatedBy string, report WerkaArchiveResponse, reportID, verifyCode, verifyURL string, y int) int {
	drawText(page, textStyle{face: fonts.bold, color: color.Black}, 70, y, "ACCORD ARCHIVE REPORT")
	y -= 54
	drawText(page, textStyle{face: fonts.title, color: color.Black}, 70, y, reportTitle)
	y -= 46
	drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, "Period: "+periodTitle)
	y -= 28
	drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, "Oraliq: "+report.From.Format("2006-01-02 15:04")+" -> "+report.To.Format("2006-01-02 15:04"))
	y -= 28
	drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, "Generated by: "+generatedBy)
	y -= 28
	drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, "Report ID: "+reportID)
	y -= 28
	drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, "Verify code: "+verifyCode)
	y -= 28
	drawMultilineText(page, textStyle{face: fonts.small, color: color.RGBA{70, 70, 70, 255}}, 70, y, "Verify URL: "+verifyURL, 1050, 22)
	return y - 40
}

func drawArchiveSummary(page *image.RGBA, fonts fontPack, summary WerkaArchiveSummary, y int) int {
	drawText(page, textStyle{face: fonts.bold, color: color.Black}, 70, y, fmt.Sprintf("Yozuvlar soni: %d", summary.RecordCount))
	y -= 34
	for _, total := range summary.TotalsByUOM {
		drawText(page, textStyle{face: fonts.body, color: color.Black}, 70, y, fmt.Sprintf("Jami: %s %.2f", strings.TrimSpace(total.UOM), total.Qty))
		y -= 26
	}
	return y - 24
}

func drawArchiveTableHeader(page *image.RGBA, fonts fontPack, y int) int {
	headerStyle := textStyle{face: fonts.bold, color: color.White}
	draw.Draw(page, image.Rect(60, y-24, 1180, y+16), &image.Uniform{color.RGBA{37, 37, 37, 255}}, image.Point{}, draw.Src)
	drawText(page, headerStyle, 70, y, "Sana")
	drawText(page, headerStyle, 250, y, "Hujjat")
	drawText(page, headerStyle, 450, y, "Counterparty")
	drawText(page, headerStyle, 760, y, "Mahsulot")
	drawText(page, headerStyle, 980, y, "Miqdor")
	drawText(page, headerStyle, 1090, y, "Status")
	return y - 54
}

func archiveRowHeight(row tableRow) int {
	partyLines := wrappedLineCount(row.party, 28)
	itemLines := wrappedLineCount(row.item+" "+row.itemName, 28)
	maxLines := partyLines
	if itemLines > maxLines {
		maxLines = itemLines
	}
	if maxLines < 2 {
		maxLines = 2
	}
	return 24*maxLines + 30
}

func drawArchiveRow(page *image.RGBA, fonts fontPack, row tableRow, y int) {
	bodyStyle := textStyle{face: fonts.body, color: color.Black}
	smallStyle := textStyle{face: fonts.small, color: color.RGBA{80, 80, 80, 255}}
	drawText(page, smallStyle, 70, y, row.date)
	drawText(page, smallStyle, 250, y, row.docID)
	drawMultilineText(page, bodyStyle, 450, y, row.party, 260, 22)
	drawMultilineText(page, bodyStyle, 760, y, row.item+" • "+row.itemName, 200, 22)
	drawText(page, bodyStyle, 980, y, row.qty)
	drawText(page, bodyStyle, 1090, y, row.status)
	rowBottom := y - archiveRowHeight(row) + 18
	draw.Draw(page, image.Rect(70, rowBottom, 1170, rowBottom+2), &image.Uniform{color.RGBA{230, 230, 230, 255}}, image.Point{}, draw.Src)
}

func drawArchiveFooter(page *image.RGBA, fonts fontPack, pageNumber int) {
	drawText(page, textStyle{face: fonts.small, color: color.RGBA{100, 100, 100, 255}}, 70, 60, fmt.Sprintf("Page %d", pageNumber))
}

func drawText(img *image.RGBA, style textStyle, x, y int, text string) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(style.color),
		Face: style.face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func drawMultilineText(img *image.RGBA, style textStyle, x, y int, text string, maxRunesPerLine, lineHeight int) {
	lines := wrapTextByRunes(text, maxRunesPerLine)
	for index, line := range lines {
		drawText(img, style, x, y-index*lineHeight, line)
	}
}

func wrapTextByRunes(text string, maxRunesPerLine int) []string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 3)
	current := words[0]
	for _, word := range words[1:] {
		candidate := current + " " + word
		if len([]rune(candidate)) <= maxRunesPerLine {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	for i := range lines {
		lines[i] = truncatePDFLine(lines[i], maxRunesPerLine)
	}
	if len(lines) > 2 {
		return []string{lines[0], truncatePDFLine(strings.Join(lines[1:], " "), maxRunesPerLine)}
	}
	return lines
}

func wrappedLineCount(text string, maxRunesPerLine int) int {
	return len(wrapTextByRunes(text, maxRunesPerLine))
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
	raw := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			offset := img.PixOffset(x, y)
			raw = append(raw, img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2])
		}
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", w, h, compressed.Len(), compressed.String())
}

func truncatePDFLine(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes-1]) + "…"
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
