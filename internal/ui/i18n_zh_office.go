package ui

import "maps"

// The Office tabs' words, kept apart from the rest; each format has a file of its own like this one.
func init() { maps.Copy(zh, zhOffice) }

var zhOffice = map[string]string{
	"Office documents":                    "Office 文档",
	"Save to a folder…":                   "保存到文件夹…",
	"Saved":                               "已保存",
	"Not saved":                           "未保存",
	"Saving…":                             "正在保存…",
	"The document could not be saved: %s": "文档无法保存：%s",
	"%s is not an Office document, whatever its name says.":                                                                                      "%s 不是 Office 文档，尽管扩展名看起来像。",
	"%s is in an old Office format. Open it in Office and save it as .docx, .xlsx or .pptx.":                                                     "%s 是旧版 Office 格式。请在 Office 中打开并另存为 .docx、.xlsx 或 .pptx。",
	"%s is protected with a password, or is in an old Office format. Open it in Office and save it without a password as .docx, .xlsx or .pptx.": "%s 受密码保护，或是旧版 Office 格式。请在 Office 中打开，去掉密码后另存为 .docx、.xlsx 或 .pptx。",
	"Write documents": "写文档",
	"Pages with styles, tables and pictures, as .docx.": "样式、表格和图片，存为 .docx",
	"New document":         "新建文档",
	"Work in spreadsheets": "做电子表格",
	"Formulas, formats and merged cells, as .xlsx.": "公式、格式和合并，存为 .xlsx",
	"New spreadsheet":                              "新建表格",
	"Make presentations":                           "做演示文稿",
	"Slides shown full screen, saved as .pptx.":    "全屏放映的幻灯片，存为 .pptx",
	"New presentation":                             "新建演示文稿",
	"Edit PDFs":                                    "编辑 PDF",
	"Mark up, fill in, sign, and rearrange pages.": "批注、填写、签名和重排页面",
	"Open a PDF…":                                  "打开 PDF…",
}
