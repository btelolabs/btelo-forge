"""Makes workbooks, slides and a document with charts through independent libraries, to look at how the app draws them.

    python chart_samples.py <folder>

Needs XlsxWriter, python-pptx and python-docx; no test depends on it.
"""
import io
import sys
import zipfile
from pathlib import Path

import docx
import xlsxwriter
from pptx import Presentation
from pptx.chart.data import CategoryChartData, XyChartData
from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION, XL_LABEL_POSITION
from pptx.util import Inches, Pt


def workbook(out):
    wb = xlsxwriter.Workbook(str(out / "charts.xlsx"))
    s = wb.add_worksheet("销售")
    rows = [["月份", "华东", "华南", "华北", "占比"], ["一月", 120, 90, 60, 0.2], ["二月", 150, 110, 40, 0.15], ["三月", 90, 130, -30, 0.25],
            ["四月", 170, 80, 75, 0.1], ["五月", 210, 140, 95, 0.1], ["六月", 180, 160, 120, 0.2]]
    for r, row in enumerate(rows):
        s.write_row(r, 0, row)
    cats = "=销售!$A$2:$A$7"

    def series(ch, cols="BCD", **kw):
        for c in cols:
            ch.add_series({"name": f"=销售!${c}$1", "categories": cats, "values": f"=销售!${c}$2:${c}$7", **kw})

    place = lambda c, r, ch: s.insert_chart(r, c, ch, {"x_scale": 0.9, "y_scale": 0.9})
    ch = wb.add_chart({"type": "column"})
    series(ch)
    ch.set_title({"name": "簇状柱形图"})
    ch.set_x_axis({"name": "月份"})
    ch.set_y_axis({"name": "销售额（万元）", "major_gridlines": {"visible": True}})
    place(6, 0, ch)
    ch = wb.add_chart({"type": "column", "subtype": "stacked"})
    series(ch, data_labels={"value": True})
    ch.set_title({"name": "堆积柱形图"})
    ch.set_legend({"position": "bottom"})
    place(14, 0, ch)
    ch = wb.add_chart({"type": "bar", "subtype": "percent_stacked"})
    series(ch, "BC")
    ch.set_title({"name": "百分比条形图"})
    place(22, 0, ch)
    ch = wb.add_chart({"type": "line"})
    series(ch, marker={"type": "circle", "size": 6})
    ch.set_title({"name": "折线图（带标记）"})
    ch.set_y_axis({"num_format": '#,##0"元"'})
    place(6, 15, ch)
    ch2 = wb.add_chart({"type": "column"})
    ch2.add_series({"name": "华东", "categories": cats, "values": "=销售!$B$2:$B$7"})
    ln = wb.add_chart({"type": "line"})
    ln.add_series({"name": "占比", "categories": cats, "values": "=销售!$E$2:$E$7", "y2_axis": True})
    ch2.combine(ln)
    ch2.set_title({"name": "组合图（次坐标轴）"})
    ln.set_y2_axis({"num_format": "0%"})
    place(14, 15, ch2)
    ch = wb.add_chart({"type": "area", "subtype": "stacked"})
    series(ch)
    ch.set_title({"name": "堆积面积图"})
    place(22, 15, ch)
    ch = wb.add_chart({"type": "pie"})
    ch.add_series({"name": "六月", "categories": "=销售!$B$1:$D$1", "values": "=销售!$B$7:$D$7",
                   "data_labels": {"percentage": True, "category": True, "leader_lines": True}})
    ch.set_title({"name": "饼图"})
    place(6, 30, ch)
    ch = wb.add_chart({"type": "doughnut"})
    ch.add_series({"categories": cats, "values": "=销售!$B$2:$B$7", "data_labels": {"value": True}})
    ch.set_title({"name": "圆环图"})
    ch.set_hole_size(55)
    place(14, 30, ch)
    ch = wb.add_chart({"type": "scatter", "subtype": "smooth_with_markers"})
    ch.add_series({"name": "散点", "categories": "=销售!$B$2:$B$7", "values": "=销售!$C$2:$C$7"})
    ch.set_title({"name": "散点图"})
    ch.set_x_axis({"name": "华东", "min": 50})
    place(22, 30, ch)
    ch = wb.add_chart({"type": "radar", "subtype": "filled"})
    series(ch, "BC")
    ch.set_title({"name": "雷达图"})
    place(6, 45, ch)
    ch = wb.add_chart({"type": "stock"})
    for c in "BCD":
        ch.add_series({"categories": cats, "values": f"=销售!${c}$2:${c}$7"})
    ch.set_title({"name": "股价图"})
    place(14, 45, ch)
    ch = wb.add_chart({"type": "column"})
    ch.add_series({"name": "华东", "categories": cats, "values": "=销售!$B$2:$B$7", "points": [{"fill": {"color": "#FF0000"}}, None, {"fill": {"color": "#00B050"}}]})
    ch.set_title({"name": "长分类名与单点颜色"})
    ch.set_y_axis({"log_base": 10})
    ch.set_x_axis({"reverse": True})
    place(22, 45, ch)
    wb.close()


def slides(out):
    p = Presentation()
    p.slide_width, p.slide_height = Inches(13.333), Inches(7.5)
    cd = CategoryChartData()
    cd.categories = ["2021", "2022", "2023", "2024"]
    cd.add_series("Revenue", (1.2, 2.4, 3.1, 4.6))
    cd.add_series("Cost", (0.8, 1.1, 1.9, 2.2))

    def add(kind, x, y, w, h, title, data=cd):
        s = p.slides[0] if p.slides else p.slides.add_slide(p.slide_layouts[5])
        g = s.shapes.add_chart(kind, Inches(x), Inches(y), Inches(w), Inches(h), data).chart
        g.has_title = True
        g.chart_title.text_frame.text = title
        return g

    s = p.slides.add_slide(p.slide_layouts[5])
    s.shapes.title.text = "Charts from python-pptx"
    g = add(XL_CHART_TYPE.COLUMN_CLUSTERED, 0.3, 1.4, 4.2, 2.9, "Clustered")
    g.has_legend = True
    g.legend.position = XL_LEGEND_POSITION.BOTTOM
    g.legend.include_in_layout = False
    g.plots[0].has_data_labels = True
    g.plots[0].data_labels.font.size = Pt(9)
    g.plots[0].data_labels.position = XL_LABEL_POSITION.OUTSIDE_END
    add(XL_CHART_TYPE.LINE_MARKERS, 4.6, 1.4, 4.2, 2.9, "Line")
    add(XL_CHART_TYPE.AREA, 8.9, 1.4, 4.2, 2.9, "Area")
    pie = CategoryChartData()
    pie.categories = ["A", "B", "C"]
    pie.add_series("Share", (5, 3, 2))
    g = add(XL_CHART_TYPE.PIE, 0.3, 4.4, 4.2, 2.9, "Pie", pie)
    g.plots[0].has_data_labels = True
    g.plots[0].data_labels.show_percentage = True
    g.plots[0].data_labels.show_value = False
    add(XL_CHART_TYPE.BAR_STACKED, 4.6, 4.4, 4.2, 2.9, "Stacked bars")
    xy = XyChartData()
    a = xy.add_series("Points")
    for x, y in ((1, 2), (2, 4.5), (3.5, 3), (5, 6)):
        a.add_data_point(x, y)
    add(XL_CHART_TYPE.XY_SCATTER_LINES, 8.9, 4.4, 4.2, 2.9, "Scatter", xy)
    p.save(out / "charts.pptx")


def document(out):
    """A document with the slides' first chart in it, inline, as Word keeps one."""
    d = docx.Document()
    d.add_heading("图表文档", 1)
    d.add_paragraph("下面是一张内嵌的图表，数据保存在图表自己的部件里。")
    marker = d.add_paragraph("CHARTHERE")
    d.add_paragraph("图表后面的正文。")
    plain = io.BytesIO()
    d.save(plain)
    with zipfile.ZipFile(out / "charts.pptx") as z:
        names = [n for n in z.namelist() if n.startswith("ppt/charts/chart") and n.endswith(".xml")]
        chart = z.read(sorted(names)[0])
    drawing = ('<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0" '
               'xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">'
               '<wp:extent cx="5400000" cy="3000000"/><wp:docPr id="50" name="Chart 1"/>'
               '<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">'
               '<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">'
               '<c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" '
               'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" r:id="rIdChart1"/>'
               '</a:graphicData></a:graphic></wp:inline></w:drawing></w:r>')
    with zipfile.ZipFile(plain) as src, zipfile.ZipFile(out / "charts.docx", "w", zipfile.ZIP_DEFLATED) as dst:
        for item in src.infolist():
            data = src.read(item.filename)
            if item.filename == "word/document.xml":
                text = data.decode("utf-8")
                start = text.rindex("<w:r>", 0, text.index("CHARTHERE"))
                end = text.index("</w:r>", start) + len("</w:r>")
                data = (text[:start] + drawing + text[end:]).encode("utf-8")
            elif item.filename == "word/_rels/document.xml.rels":
                data = data.replace(b"</Relationships>", b'<Relationship Id="rIdChart1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/chart1.xml"/></Relationships>')
            elif item.filename == "[Content_Types].xml":
                data = data.replace(b"</Types>", b'<Override PartName="/word/charts/chart1.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.chart+xml"/></Types>')
            dst.writestr(item, data)
        dst.writestr("word/charts/chart1.xml", chart)


def main():
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    workbook(out)
    slides(out)
    document(out)


if __name__ == "__main__":
    main()
