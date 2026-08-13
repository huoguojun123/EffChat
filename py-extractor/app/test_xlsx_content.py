from __future__ import annotations

import io
import unittest
import zipfile
from datetime import date
from xml.etree import ElementTree

from openpyxl import Workbook
from openpyxl.worksheet.formula import ArrayFormula

from app import main, xlsx_content


MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
NS = {"main": MAIN_NS}


def workbook_bytes() -> bytes:
    buffer = io.BytesIO()
    workbook = Workbook()
    first = workbook.active
    first.title = "Metrics"
    first.append(["Metric", "Value", "Notes"])
    first.append(["Total", "=SUM(1,2)", "formula source"])
    first.append(["Plain", 7, "ordinary value"])
    first.append(["Blank", None, "empty cell"])
    first.append(["Shared", "=ROW()+1", "=ROW()+2"])
    first.append(["Error", "=1/0", "错误缓存"])
    first.append(["日期", date(2026, 8, 11), "Unicode value"])
    second = workbook.create_sheet("Array")
    second["A1"] = ArrayFormula(ref="A1:B1", text="=TRANSPOSE({1,2})")
    second["B1"] = ArrayFormula(ref="A1:B1", text="=")
    workbook.save(buffer)
    workbook.close()
    return buffer.getvalue()


def with_cached_values(
    data: bytes,
    sheet_name: str,
    values: dict[str, str],
    *,
    error_cells: set[str] | None = None,
) -> bytes:
    output = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(output, "w") as target:
        for name in source.namelist():
            raw = source.read(name)
            if name == f"xl/worksheets/{sheet_name}.xml":
                root = ElementTree.fromstring(raw)
                for cell in root.findall(".//main:c", NS):
                    coordinate = cell.attrib.get("r")
                    if coordinate not in values:
                        continue
                    if coordinate in (error_cells or set()):
                        cell.set("t", "e")
                    value = cell.find("main:v", NS)
                    if value is None:
                        value = ElementTree.SubElement(cell, f"{{{MAIN_NS}}}v")
                    value.text = values[coordinate]
                raw = ElementTree.tostring(root, encoding="utf-8", xml_declaration=True)
            target.writestr(name, raw)
    return output.getvalue()


def with_shared_formula(data: bytes) -> bytes:
    output = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(data)) as source, zipfile.ZipFile(output, "w") as target:
        for name in source.namelist():
            raw = source.read(name)
            if name == "xl/worksheets/sheet1.xml":
                root = ElementTree.fromstring(raw)
                cells = {
                    cell.attrib.get("r"): cell
                    for cell in root.findall(".//main:c", NS)
                }
                for coordinate, shared_index, formula, ref in (
                    ("B5", "0", "ROW()+1", "B5:C5"),
                    ("C5", "0", None, None),
                ):
                    formula_node = cells[coordinate].find("main:f", NS)
                    formula_node.attrib.clear()
                    formula_node.set("t", "shared")
                    formula_node.set("si", shared_index)
                    if ref:
                        formula_node.set("ref", ref)
                    formula_node.text = formula
                    value = cells[coordinate].find("main:v", NS)
                    if value is None:
                        value = ElementTree.SubElement(cells[coordinate], f"{{{MAIN_NS}}}v")
                    value.text = "6" if coordinate == "B5" else "7"
                raw = ElementTree.tostring(root, encoding="utf-8", xml_declaration=True)
            target.writestr(name, raw)
    return output.getvalue()


class XlsxFormulaFidelityTest(unittest.TestCase):
    def test_uncached_formula_is_not_silently_empty(self):
        extracted = main.extract_xlsx(workbook_bytes(), "formula.xlsx")

        self.assertIn("=SUM(1,2) [no cached value]", extracted.text)
        self.assertIn("ordinary value", extracted.text)
        self.assertNotIn("Total |  |", extracted.text)

    def test_cached_formula_keeps_source_and_cached_value(self):
        extracted = main.extract_xlsx(
            with_cached_values(workbook_bytes(), "sheet1", {"B2": "3"}),
            "formula.xlsx",
        )

        self.assertIn("=SUM(1,2) [cached value: 3]", extracted.text)

    def test_array_formula_cells_are_searchable_and_distinguishable(self):
        extracted = main.extract_xlsx(
            with_cached_values(workbook_bytes(), "sheet2", {"A1": "1", "B1": "2"}),
            "array.xlsx",
        )

        self.assertIn("=TRANSPOSE({1,2}) [array cell A1 of A1:B1] [cached value: 1]", extracted.text)
        self.assertIn("=TRANSPOSE({1,2}) [array cell B1 of A1:B1] [cached value: 2]", extracted.text)

    def test_shared_formula_cells_keep_the_source_expression(self):
        extracted = main.extract_xlsx(with_shared_formula(workbook_bytes()), "shared.xlsx")

        self.assertIn("=ROW()+1 [cached value: 6]", extracted.text)
        self.assertIn("=ROW()+1 [cached value: 7]", extracted.text)

    def test_error_cache_dates_unicode_and_empty_cells_remain_distinct(self):
        extracted = main.extract_xlsx(
            with_cached_values(
                workbook_bytes(),
                "sheet1",
                {"B6": "#DIV/0!"},
                error_cells={"B6"},
            ),
            "types.xlsx",
        )

        self.assertIn("=1/0 [cached value: #DIV/0!]", extracted.text)
        self.assertIn("| Blank |  | empty cell |", extracted.text)
        self.assertIn("| 日期 | 2026-08-11 00:00:00 | Unicode value |", extracted.text)

    def test_content_module_matches_main_contract(self):
        content = xlsx_content.extract(workbook_bytes())

        self.assertEqual(content.table_count, 2)
        self.assertEqual(content.paragraph_count, 2)
        self.assertIn("## Metrics", content.text)
        self.assertIn("## Array", content.text)


if __name__ == "__main__":
    unittest.main()
