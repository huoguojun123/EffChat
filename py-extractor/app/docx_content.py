from __future__ import annotations

import io
from dataclasses import dataclass

from docx import Document
from docx.document import Document as DocumentObject
from docx.oxml.table import CT_Tbl
from docx.oxml.text.paragraph import CT_P
from docx.table import Table
from docx.text.paragraph import Paragraph

from app import markdown_table


@dataclass
class DocxContent:
    text: str
    paragraph_count: int
    table_count: int


def extract(data: bytes) -> DocxContent:
    document = Document(io.BytesIO(data))
    blocks: list[str] = []
    paragraph_count = 0
    table_count = 0
    for block in _body_blocks(document):
        if isinstance(block, Paragraph):
            text = block.text.strip()
            if text:
                blocks.append(text)
                paragraph_count += 1
            continue
        rows = [[cell.text.strip() for cell in row.cells] for row in block.rows]
        table = markdown_table.serialize(rows)
        if table:
            blocks.append(table)
            table_count += 1
    return DocxContent(
        text="\n\n".join(blocks),
        paragraph_count=paragraph_count,
        table_count=table_count,
    )


def _body_blocks(document: DocumentObject):
    """Yield top-level paragraphs and tables in their Word body order."""
    for child in document.element.body.iterchildren():
        if isinstance(child, CT_P):
            yield Paragraph(child, document)
        elif isinstance(child, CT_Tbl):
            yield Table(child, document)
