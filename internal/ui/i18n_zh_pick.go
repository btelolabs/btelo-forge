package ui

import "maps"

// The file picker's words, kept apart from the rest.
func init() { maps.Copy(zh, zhPick) }

var zhPick = map[string]string{
	"From the system's file manager…": "从系统文件管理器选择…",
	"Choose this folder":              "选择此文件夹",
	"File name":                       "文件名",
	"Replace %s?":                     "替换 %s？",
	"A file of that name is in this folder; it will be written over.": "此文件夹里已有同名文件，它会被覆盖。",
	`A file name cannot hold any of < > : " / \ | ? *`:                `文件名不能包含 < > : " / \ | ? *`,
	"Save the 3D file": "保存 3D 文件",
}
