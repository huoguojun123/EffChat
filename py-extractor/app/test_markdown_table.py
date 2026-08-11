from __future__ import annotations

import unittest

from app import markdown_table


class MarkdownTableTest(unittest.TestCase):
    def test_gfm_structure_characters_and_irregular_rows(self):
        rendered = markdown_table.serialize(
            [
                ["head|er", r"literal\|pipe", "multi\r\nline", "<tag>", "中文"],
                ["A", "B\rC", "D\nE"],
                [None, "", "tail", "&entity;", "🙂"],
            ]
        )
        self.assertEqual(
            rendered,
            "\n".join(
                [
                    r"| head\|er | literal\\\|pipe | multi&#10;line | &lt;tag&gt; | 中文 |",
                    "| --- | --- | --- | --- | --- |",
                    "| A | B&#10;C | D&#10;E |  |  |",
                    "|  |  | tail | &amp;entity; | 🙂 |",
                ]
            ),
        )

    def test_empty_rows_and_header_only_table(self):
        self.assertEqual(markdown_table.serialize([[None], [""]]), "")
        self.assertEqual(markdown_table.serialize([["header"]]), "| header |\n| --- |\n|  |")


if __name__ == "__main__":
    unittest.main()
