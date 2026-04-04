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
	title      font.Face
	subtitle   font.Face
	body       font.Face
	bodyTight  font.Face
	small      font.Face
	smallTight font.Face
	bold       font.Face
	boldTight  font.Face
	watermark  font.Face
}

type archiveColumn struct {
	label string
	x     int
	width int
}

var (
	dateColumn    = archiveColumn{label: "Sana", x: 60, width: 150}
	docColumn     = archiveColumn{label: "Hujjat", x: 210, width: 185}
	partyColumn   = archiveColumn{label: "Counterparty", x: 395, width: 165}
	productColumn = archiveColumn{label: "Mahsulot", x: 560, width: 425}
	qtyColumn     = archiveColumn{label: "Miqdor", x: 985, width: 110}
	statusColumn  = archiveColumn{label: "Status", x: 1095, width: 85}
)

var archiveColumns = []archiveColumn{
	dateColumn,
	docColumn,
	partyColumn,
	productColumn,
	qtyColumn,
	statusColumn,
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
	for index, row := range rows {
		height := archiveRowHeight(row, fonts)
		if y+height > 1630 {
			drawArchiveFooter(page, fonts, len(pages)+1)
			pages = append(pages, page)
			page, y = newArchivePage(pageWidth, pageHeight)
			drawArchiveWatermark(page, fonts)
			y = drawArchiveHeader(page, fonts, reportTitle, periodTitle, generatedBy, report, reportID, verifyCode, verifyURL, y)
			y = drawArchiveSummary(page, fonts, report.Summary, y)
			y = drawArchiveTableHeader(page, fonts, y)
		}
		drawArchiveRow(page, fonts, row, y, index%2 == 0)
		y += height + 8
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
	title, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 28, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	subtitle, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 20, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	body, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 12, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	bodyTight, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 10, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	small, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 10, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	smallTight, err := opentype.NewFace(regularTTF, &opentype.FaceOptions{Size: 8, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	bold, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 12, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	boldTight, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 10, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	watermark, err := opentype.NewFace(boldTTF, &opentype.FaceOptions{Size: 38, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return fontPack{}, err
	}
	return fontPack{
		title:      title,
		subtitle:   subtitle,
		body:       body,
		bodyTight:  bodyTight,
		small:      small,
		smallTight: smallTight,
		bold:       bold,
		boldTight:  boldTight,
		watermark:  watermark,
	}, nil
}

func newArchivePage(pageWidth, pageHeight int) (*image.RGBA, int) {
	page := image.NewRGBA(image.Rect(0, 0, pageWidth, pageHeight))
	draw.Draw(page, page.Bounds(), &image.Uniform{color.RGBA{250, 248, 244, 255}}, image.Point{}, draw.Src)
	return page, 88
}

func drawArchiveWatermark(page *image.RGBA, fonts fontPack) {
	watermarkStyle := textStyle{
		face:  fonts.watermark,
		color: color.RGBA{140, 128, 108, 8},
	}
	drawText(page, watermarkStyle, 340, 1540, "ACCORD ARCHIVE")
}

func drawArchiveHeader(page *image.RGBA, fonts fontPack, reportTitle, periodTitle, generatedBy string, report WerkaArchiveResponse, reportID, verifyCode, verifyURL string, y int) int {
	fillRect(page, 60, y, 1180, 260, color.RGBA{31, 37, 43, 255})
	fillRect(page, 60, y, 1180, 14, color.RGBA{201, 167, 104, 255})

	light := color.RGBA{247, 243, 234, 255}
	muted := color.RGBA{222, 214, 198, 255}
	drawText(page, textStyle{face: fonts.bold, color: muted}, 86, y+52, "ACCORD ARCHIVE REPORT")
	drawText(page, textStyle{face: fonts.title, color: light}, 86, y+98, reportTitle)
	drawText(page, textStyle{face: fonts.body, color: light}, 86, y+138, "Period: "+periodTitle)
	drawText(page, textStyle{face: fonts.body, color: light}, 86, y+168, "Oraliq: "+report.From.Format("2006-01-02 15:04")+" -> "+report.To.Format("2006-01-02 15:04"))
	drawText(page, textStyle{face: fonts.body, color: light}, 86, y+198, "Generated by: "+generatedBy)

	fillRect(page, 805, y+34, 350, 188, color.RGBA{247, 243, 234, 255})
	dark := color.RGBA{31, 37, 43, 255}
	drawText(page, textStyle{face: fonts.bold, color: dark}, 830, y+74, "Compliance Panel")
	drawText(page, textStyle{face: fonts.small, color: dark}, 830, y+106, "Report ID: "+reportID)
	drawText(page, textStyle{face: fonts.small, color: dark}, 830, y+132, "Verify code: "+verifyCode)
	drawMultilineText(page, textStyle{face: fonts.small, color: color.RGBA{80, 80, 80, 255}}, 830, y+160, "Verify URL: "+verifyURL, 270, 18)
	return y + 290
}

func drawArchiveSummary(page *image.RGBA, fonts fontPack, summary WerkaArchiveSummary, y int) int {
	fillRect(page, 60, y, 1180, 118, color.RGBA{244, 238, 227, 255})
	drawText(page, textStyle{face: fonts.bold, color: color.Black}, 82, y+42, fmt.Sprintf("Yozuvlar soni: %d", summary.RecordCount))
	x := 360
	for _, total := range summary.TotalsByUOM {
		fillRect(page, x, y+18, 220, 54, color.RGBA{255, 255, 255, 255})
		drawText(page, textStyle{face: fonts.body, color: color.Black}, x+18, y+52, fmt.Sprintf("%s: %.2f", strings.TrimSpace(total.UOM), total.Qty))
		x += 240
	}
	return y + 144
}

func drawArchiveTableHeader(page *image.RGBA, fonts fontPack, y int) int {
	headerBg := color.RGBA{53, 67, 89, 255}
	border := color.RGBA{99, 116, 142, 255}
	headerStyle := textStyle{face: fonts.bold, color: color.White}
	for _, col := range archiveColumns {
		drawCellBox(page, col, y, 58, headerBg, border)
		drawSingleCellLine(page, headerStyle, col, y, 58, col.label)
	}
	return y + 58
}

func archiveRowHeight(row tableRow, fonts fontPack) int {
	_ = row
	_ = fonts
	return 46
}

func drawArchiveRow(page *image.RGBA, fonts fontPack, row tableRow, y int, zebra bool) {
	height := archiveRowHeight(row, fonts)
	rowBg := color.RGBA{255, 255, 255, 255}
	if zebra {
		rowBg = color.RGBA{245, 247, 251, 255}
	}
	grid := color.RGBA{219, 225, 236, 255}
	statusBg := color.RGBA{255, 244, 204, 255}
	dateStyle := textStyle{face: fonts.smallTight, color: color.RGBA{67, 73, 84, 255}}
	bodyStyle := textStyle{face: fonts.bodyTight, color: color.RGBA{39, 43, 52, 255}}
	qtyStyle := textStyle{face: fonts.bodyTight, color: color.RGBA{39, 43, 52, 255}}
	statusStyle := textStyle{face: fonts.bodyTight, color: color.RGBA{158, 110, 0, 255}}

	drawCellBox(page, dateColumn, y, height, rowBg, grid)
	drawCellBox(page, docColumn, y, height, rowBg, grid)
	drawCellBox(page, partyColumn, y, height, rowBg, grid)
	drawCellBox(page, productColumn, y, height, rowBg, grid)
	drawCellBox(page, qtyColumn, y, height, rowBg, grid)
	drawCellBox(page, statusColumn, y, height, statusBg, grid)

	drawSingleCellLine(page, dateStyle, dateColumn, y, height, compactArchiveDate(row.date))
	drawSingleCellLine(page, bodyStyle, docColumn, y, height, row.docID)
	drawSingleCellLine(page, bodyStyle, partyColumn, y, height, row.party)
	drawSingleCellLine(page, bodyStyle, productColumn, y, height, archiveProductLine(row))
	drawSingleCellLine(page, qtyStyle, qtyColumn, y, height, row.qty)
	drawSingleCellLine(page, statusStyle, statusColumn, y, height, formatArchiveStatusLabel(row.status))
}

func drawArchiveFooter(page *image.RGBA, fonts fontPack, pageNumber int) {
	fillRect(page, 60, 1664, 1180, 30, color.RGBA{31, 37, 43, 255})
	drawText(page, textStyle{face: fonts.small, color: color.RGBA{244, 238, 227, 255}}, 82, 1686, fmt.Sprintf("Page %d", pageNumber))
	drawText(page, textStyle{face: fonts.small, color: color.RGBA{210, 202, 186, 255}}, 980, 1686, "Protected archive export")
}

func drawStatusPill(page *image.RGBA, fonts fontPack, x, y int, value string) {
	w := 150
	fillRect(page, x, y, w, 34, statusPillColor(value))
	drawText(page, textStyle{face: fonts.small, color: color.White}, x+16, y+22, value)
}

func statusPillColor(value string) color.RGBA {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending":
		return color.RGBA{173, 113, 37, 255}
	case "accepted":
		return color.RGBA{46, 125, 50, 255}
	case "partial":
		return color.RGBA{2, 119, 189, 255}
	case "rejected", "cancelled":
		return color.RGBA{183, 28, 28, 255}
	default:
		return color.RGBA{84, 84, 84, 255}
	}
}

func formatArchiveStatusLabel(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "-"
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
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
		drawText(img, style, x, y+index*lineHeight, line)
	}
}

func drawCellBox(page *image.RGBA, col archiveColumn, y, height int, fill, border color.Color) {
	fillRect(page, col.x, y, col.width, height, fill)
	fillRect(page, col.x, y, col.width, 1, border)
	fillRect(page, col.x, y+height-1, col.width, 1, border)
	fillRect(page, col.x, y, 1, height, border)
	fillRect(page, col.x+col.width-1, y, 1, height, border)
}

func drawSingleCellLine(page *image.RGBA, style textStyle, col archiveColumn, y, height int, value string) {
	drawer := &font.Drawer{Face: style.face}
	fitted := fitStringToWidth(drawer, value, col.width-24)
	textY := y + (height / 2) + 4
	drawText(page, style, col.x+12, textY, fitted)
}

func drawCellLines(page *image.RGBA, style textStyle, col archiveColumn, y int, lines []string) {
	lines = filterNonEmptyLines(lines)
	if len(lines) == 0 {
		return
	}
	drawer := &font.Drawer{Face: style.face}
	for index, line := range lines {
		fitted := fitStringToWidth(drawer, line, col.width-28)
		drawText(page, style, col.x+14, y+28+index*22, fitted)
	}
}

func drawCellText(page *image.RGBA, style textStyle, col archiveColumn, y, height int, text string, maxLines, lineHeight int) {
	lines := wrapTextByWidth(style.face, text, col.width-28, maxLines)
	for index, line := range lines {
		drawText(
			page,
			style,
			col.x+14,
			y+24+index*lineHeight,
			line,
		)
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

func wrapTextByWidth(face font.Face, text string, maxWidth, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 1
	}
	paragraphs := strings.Split(strings.TrimSpace(text), "\n")
	lines := make([]string, 0, maxLines)
	drawer := &font.Drawer{Face: face}
	for _, paragraph := range paragraphs {
		words := strings.Fields(strings.TrimSpace(paragraph))
		if len(words) == 0 {
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			candidate := current + " " + word
			if drawer.MeasureString(candidate).Ceil() <= maxWidth {
				current = candidate
				continue
			}
			lines = append(lines, fitStringToWidth(drawer, current, maxWidth))
			current = word
			if len(lines) >= maxLines {
				break
			}
		}
		if len(lines) < maxLines {
			lines = append(lines, fitStringToWidth(drawer, current, maxWidth))
		}
		if len(lines) >= maxLines {
			break
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

func fitStringToWidth(drawer *font.Drawer, value string, maxWidth int) string {
	trimmed := strings.TrimSpace(value)
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

func formatArchiveDate(value string) string {
	trimmed := strings.TrimSpace(value)
	if idx := strings.Index(trimmed, " "); idx > 0 {
		return trimmed[:idx] + "\n" + strings.TrimSpace(trimmed[idx+1:])
	}
	return trimmed
}

func compactArchiveDate(value string) string {
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

func archiveProductLine(row tableRow) string {
	itemName := strings.TrimSpace(row.itemName)
	itemCode := strings.TrimSpace(row.item)
	if itemName == "" {
		return itemCode
	}
	if itemCode == "" || strings.EqualFold(itemName, itemCode) {
		return itemName
	}
	return itemName + " • " + itemCode
}

func splitArchiveDate(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if idx := strings.Index(trimmed, " "); idx > 0 {
		return trimmed[:idx], strings.TrimSpace(trimmed[idx+1:])
	}
	return trimmed, ""
}

func archivePrimaryTitle(row tableRow) string {
	itemName := strings.TrimSpace(row.itemName)
	itemCode := strings.TrimSpace(row.item)
	party := strings.TrimSpace(row.party)
	if itemName != "" && !strings.EqualFold(itemName, party) {
		return itemName
	}
	if itemCode != "" && !strings.EqualFold(itemCode, party) {
		return itemCode
	}
	if itemName != "" {
		return itemName
	}
	if itemCode != "" {
		return itemCode
	}
	return party
}

func archiveSecondaryLine1(row tableRow, primary string) string {
	party := strings.TrimSpace(row.party)
	if party != "" && !strings.EqualFold(party, primary) {
		return party
	}
	return ""
}

func archiveSecondaryLine2(row tableRow, primary string) string {
	itemCode := strings.TrimSpace(row.item)
	party := strings.TrimSpace(row.party)
	if itemCode != "" &&
		!strings.EqualFold(itemCode, primary) &&
		!strings.EqualFold(itemCode, party) {
		return itemCode
	}
	return ""
}

func filterNonEmptyLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.Color) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
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
