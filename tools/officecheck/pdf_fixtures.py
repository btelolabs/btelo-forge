"""Writes the encrypted test files of internal/office/pdf/testdata with pypdf. Usage: pdf_fixtures.py OUTDIR"""
import sys, os
from pypdf import PdfWriter
from pypdf.generic import DecodedStreamObject, NameObject, DictionaryObject

out = sys.argv[1]
os.makedirs(out, exist_ok=True)


def page(w):
    p = w.add_blank_page(200, 200)
    s = DecodedStreamObject()
    s.set_data(b"BT /F1 12 Tf 20 100 Td (Secret page) Tj ET")
    p[NameObject("/Contents")] = w._add_object(s)
    return p


for name, algo in [("rc4-40", "RC4-40"), ("rc4-128", "RC4-128"), ("aes-128", "AES-128"), ("aes-256", "AES-256"), ("aes-256-r5", "AES-256-R5")]:
    for pw, label in [("", "empty"), ("secret", "pw")]:
        w = PdfWriter()
        page(w)
        w.encrypt(user_password=pw, owner_password="owner", algorithm=algo)
        w.write(os.path.join(out, f"enc-{name}-{label}.pdf"))
