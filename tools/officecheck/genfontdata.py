"""Generates the data files of internal/office/pdf/font.

Sources: the installed pypdf (_codecs, BSD licence) for the glyph list, the
PDFDoc, Windows and Mac tables, and the Adobe Core 14 AFM files shipped with
matplotlib for glyph names and widths. pypdf keeps widths under characters and
lost the Zapf Dingbats names, so the AFM files are the source there and pypdf
is the cross-check. Run it with no arguments from any folder.
"""

import os
import re

import matplotlib
import pypdf._codecs as codecs
from pypdf._codecs.core_font_metrics import CORE_FONT_METRICS

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "internal", "office", "pdf", "font")
AFM = os.path.join(os.path.dirname(matplotlib.__file__), "mpl-data", "fonts", "pdfcorefonts")
FONTS = [
    "Courier", "Courier-Bold", "Courier-BoldOblique", "Courier-Oblique",
    "Helvetica", "Helvetica-Bold", "Helvetica-BoldOblique", "Helvetica-Oblique",
    "Times-Roman", "Times-Bold", "Times-BoldItalic", "Times-Italic",
    "Symbol", "ZapfDingbats",
]


def read_afm(name):
    info = {"name": name, "codes": {}, "widths": {}}
    with open(os.path.join(AFM, name + ".afm"), encoding="latin-1") as f:
        for line in f:
            parts = line.split()
            if not parts:
                continue
            if parts[0] in ("Ascender", "Descender", "CapHeight"):
                info[parts[0]] = float(parts[1])
            elif parts[0] == "FontBBox":
                info["bbox"] = [float(v) for v in parts[1:5]]
            elif parts[0] == "C":
                m = re.match(r"C (-?\d+) ; WX (-?\d+) ; N (\S+) ;", line)
                code, width, glyph = int(m[1]), int(m[2]), m[3]
                info["widths"][glyph] = width
                if code >= 0:
                    info["codes"][code] = glyph
    return info


afm = {name: read_afm(name) for name in FONTS}


def read_glyphs():
    """Reads the Adobe list and the TeX names, leaving out pypdf's other sections.

    The file's glyphNamesToUnicode and "old names" sections hold names such as
    a32 for a space, which are character codes and clash with Zapf Dingbats.
    """
    source = os.path.join(os.path.dirname(codecs.__file__), "adobe_glyphs.py")
    keep = False
    found = {}
    with open(source, encoding="utf-8") as f:
        for line in f:
            text = line.strip()
            if text.startswith("#"):
                if "glyphlist.txt" in text or "cmex10.afm" in text:
                    keep = True
                elif "glyphNamesToUnicode" in text or "Missing old names" in text:
                    keep = False
                continue
            m = re.match(r'"/([^"]+)": "((?:\\u[0-9A-Fa-f]{4})+)"', text)
            if keep and m:
                found[m[1]] = m[2].encode("ascii").decode("unicode_escape")
    return found


glyphs = read_glyphs()
for code, glyph in afm["ZapfDingbats"]["codes"].items():
    if code > 32:
        glyphs[glyph] = codecs._zapfding_encoding[code]

# The canonical name of a character is a core font's name when there is one.
core = set()
for info in afm.values():
    core.update(info["widths"])


def rank(name):
    return (name not in core, name.startswith("afii"), re.match(r"(SF|H)\d", name) is not None, len(name), name)


by_rune = {}
multi = []
for name, text in glyphs.items():
    if len(text) == 1:
        by_rune.setdefault(ord(text), []).append(name)
    elif len(text) > 1:
        multi.append((ord(text[0]), name))
canon = {r: min(names, key=rank) for r, names in by_rune.items()}

with open(os.path.join(OUT, "glyphlist.txt"), "w", encoding="ascii", newline="\n") as f:
    for r in sorted(by_rune):
        f.write("%04X %s\n" % (r, " ".join(sorted(by_rune[r], key=rank))))
    for r, name in sorted(multi):
        f.write("+%04X %s\n" % (r, name))


def named(table):
    return [canon.get(ord(c), "") if c != "\x00" else "" for c in table]


standard = [""] * 256
for code, glyph in afm["Times-Roman"]["codes"].items():
    standard[code] = glyph
for code, c in enumerate(codecs._std_encoding):
    if standard[code] and glyphs[standard[code]] != c:
        print("standard differs", code, standard[code], ascii(c))

win = named(codecs._win_encoding)
for code in range(32):
    win[code] = ""
for code in (0x7F, 0x81, 0x8D, 0x8F, 0x90, 0x9D):
    win[code] = "bullet"  # ISO 32000 annex D: unused codes above 40 octal.
win[0xA0] = "space"
win[0xAD] = "hyphen"

mac = named(codecs._mac_encoding)
for code in list(range(32)) + [0x7F]:
    mac[code] = ""
mac[0xCA] = "space"
mac[0xDB] = "currency"  # The codec has the later euro sign.
mac[0xBD] = "Omega"  # The codec has the Greek letter, the list the ohm sign.

symbol = [""] * 256
for code, glyph in afm["Symbol"]["codes"].items():
    symbol[code] = glyph
zapf = [""] * 256
for code, glyph in afm["ZapfDingbats"]["codes"].items():
    zapf[code] = glyph

# ISO 32000 annex D.4, written from knowledge of the table as octal codes.
mac_expert_octal = """
AEsmall 276 Aacutesmall 207 Acircumflexsmall 211 Acutesmall 047 Adieresissmall 212 Agravesmall 210
Aringsmall 214 Asmall 141 Atildesmall 213 Brevesmall 363 Bsmall 142 Caronsmall 256 Ccedillasmall 215
Cedillasmall 311 Circumflexsmall 136 Csmall 143 Dieresissmall 254 Dotaccentsmall 372 Dsmall 144
Eacutesmall 216 Ecircumflexsmall 220 Edieresissmall 221 Egravesmall 217 Esmall 145 Ethsmall 104
Fsmall 146 Gravesmall 140 Gsmall 147 Hsmall 150 Hungarumlautsmall 042 Iacutesmall 222
Icircumflexsmall 224 Idieresissmall 225 Igravesmall 223 Ismall 151 Jsmall 152 Ksmall 153
Lslashsmall 302 Lsmall 154 Macronsmall 364 Msmall 155 Nsmall 156 Ntildesmall 226 OEsmall 317
Oacutesmall 227 Ocircumflexsmall 231 Odieresissmall 232 Ogoneksmall 362 Ogravesmall 230
Oslashsmall 277 Osmall 157 Otildesmall 233 Psmall 160 Qsmall 161 Ringsmall 373 Rsmall 162
Scaronsmall 247 Ssmall 163 Thornsmall 271 Tildesmall 176 Tsmall 164 Uacutesmall 234
Ucircumflexsmall 236 Udieresissmall 237 Ugravesmall 235 Usmall 165 Vsmall 166 Wsmall 167 Xsmall 170
Yacutesmall 264 Ydieresissmall 330 Ysmall 171 Zcaronsmall 275 Zsmall 172 ampersandsmall 046
asuperior 201 bsuperior 365 centinferior 251 centoldstyle 043 centsuperior 202 colon 072
colonmonetary 173 comma 054 commainferior 262 commasuperior 370 dollarinferior 266
dollaroldstyle 044 dollarsuperior 045 dsuperior 353 eightinferior 245 eightoldstyle 070
eightsuperior 241 esuperior 344 exclamdownsmall 326 exclamsmall 041 ff 126 ffi 131 ffl 132 fi 127
figuredash 320 fiveeighths 114 fiveinferior 260 fiveoldstyle 065 fivesuperior 336 fl 130
fourinferior 242 fouroldstyle 064 foursuperior 335 fraction 057 hyphen 055 hypheninferior 137
hyphensuperior 321 isuperior 351 lsuperior 361 msuperior 367 nineinferior 273 nineoldstyle 071
ninesuperior 341 nsuperior 366 onedotenleader 053 oneeighth 112 onefitted 174 onehalf 110
oneinferior 301 oneoldstyle 061 onequarter 107 onesuperior 332 onethird 116 osuperior 257
parenleftinferior 133 parenleftsuperior 050 parenrightinferior 135 parenrightsuperior 051
period 056 periodinferior 263 periodsuperior 371 questiondownsmall 300 questionsmall 077
rsuperior 345 rupiah 175 semicolon 073 seveneighths 115 seveninferior 246 sevenoldstyle 067
sevensuperior 340 sixinferior 244 sixoldstyle 066 sixsuperior 337 space 040 ssuperior 352
threeeighths 113 threeinferior 243 threeoldstyle 063 threequarters 111 threequartersemdash 075
threesuperior 334 tsuperior 346 twodotenleader 052 twoinferior 252 twooldstyle 062
twosuperior 333 twothirds 117 zeroinferior 274 zerooldstyle 060 zerosuperior 342
""".split()
mac_expert = [""] * 256
for i in range(0, len(mac_expert_octal), 2):
    code = int(mac_expert_octal[i + 1], 8)
    assert not mac_expert[code], code
    mac_expert[code] = mac_expert_octal[i]

pdfdoc = [ord(c) for c in codecs._pdfdoc_encoding]
pdfdoc[0x16] = 0x17  # pypdf's own note on Table D.2.


def go_names(name, table):
    out = ["var %s = [256]string{" % name]
    line = ""
    for code, glyph in enumerate(table):
        if not glyph:
            continue
        item = '%d: "%s", ' % (code, glyph)
        if len(line) + len(item) > 104:
            out.append("\t" + line.rstrip())
            line = ""
        line += item
    out.append("\t" + line.rstrip())
    out.append("}")
    return "\n".join(out) + "\n"


with open(os.path.join(OUT, "encodings_gen.go"), "w", encoding="ascii", newline="\n") as f:
    f.write("// Code generated by tools/officecheck/genfontdata.py. DO NOT EDIT.\n\n")
    f.write("// Data from pypdf _codecs (BSD licence: std.py, pdfdoc.py, cp1252 and mac_roman tables),\n")
    f.write("// the Adobe Core 14 AFM files (glyph names) and ISO 32000 annex D (MacExpertEncoding).\n\n")
    f.write("package font\n\n")
    f.write("// StandardEncoding is Adobe StandardEncoding.\n" + go_names("StandardEncoding", standard) + "\n")
    f.write("// WinAnsiEncoding is ISO 32000 WinAnsiEncoding, unused codes being bullet.\n" + go_names("WinAnsiEncoding", win) + "\n")
    f.write("// MacRomanEncoding is ISO 32000 MacRomanEncoding plus the Mac OS Roman mathematical glyphs.\n" + go_names("MacRomanEncoding", mac) + "\n")
    f.write("// SymbolEncoding is the built-in encoding of the Symbol font.\n" + go_names("SymbolEncoding", symbol) + "\n")
    f.write("// ZapfDingbatsEncoding is the built-in encoding of the ZapfDingbats font.\n" + go_names("ZapfDingbatsEncoding", zapf) + "\n")
    f.write("// MacExpertEncoding is ISO 32000 MacExpertEncoding.\n" + go_names("MacExpertEncoding", mac_expert) + "\n")
    f.write("// PDFDocEncoding maps PDFDocEncoding bytes to Unicode, 0 for undefined codes.\n")
    f.write("var PDFDocEncoding = [256]rune{\n")
    for start in range(0, 256, 12):
        f.write("\t" + " ".join("0x%04X," % v for v in pdfdoc[start:start + 12]) + "\n")
    f.write("}\n")

mismatch = 0
with open(os.path.join(OUT, "standard14.txt"), "w", encoding="ascii", newline="\n") as f:
    for name in FONTS:
        info = afm[name]
        bbox = info["bbox"]
        ascent = info.get("Ascender", bbox[3])
        descent = info.get("Descender", bbox[1])
        cap = info.get("CapHeight", bbox[3])
        f.write("%s %g %g %g %g %g %g %g\n" % (name, ascent, descent, cap, *bbox))
        f.write(" ".join("%s %d" % item for item in sorted(info["widths"].items())) + "\n")
        if name in ("Symbol", "ZapfDingbats"):
            continue
        widths = CORE_FONT_METRICS[name].character_widths
        for glyph, width in info["widths"].items():
            c = glyphs.get(glyph)
            if c in widths and widths[c] != width:
                mismatch += 1
                print("width differs", name, glyph, width, widths[c])
print("width mismatches against pypdf:", mismatch)
print("MacExpert entries:", sum(1 for g in mac_expert if g))
