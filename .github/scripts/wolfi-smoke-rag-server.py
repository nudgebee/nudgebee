# rag-server native/ML deps. magic.from_buffer exercises libmagic via ctypes
# (a runtime dlopen ldd cannot see) — the Wolfi gap this whole check exists for.
# Referencing each module confirms it loaded and satisfies unused-import linters.
import numpy
import psycopg2
import onnxruntime
import fitz
import unstructured
import torch
import sentence_transformers
import magic

magic.from_buffer(b"%PDF-1.4 minimal test buffer")

modules = [numpy, psycopg2, onnxruntime, fitz, unstructured,
           torch, sentence_transformers, magic]
print("ML_IMPORTS_OK rag:",
      ", ".join(m.__name__ for m in modules),
      "| torch", torch.__version__)
