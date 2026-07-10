# rag-server native/ML deps. magic.from_buffer exercises libmagic via ctypes
# (a runtime dlopen ldd cannot see) — the classic Wolfi gap.
import numpy, psycopg2, onnxruntime, fitz, unstructured  # noqa: F401
import torch  # noqa: F401
import sentence_transformers  # noqa: F401
import magic
magic.from_buffer(b"%PDF-1.4 minimal test buffer")
print("ML_IMPORTS_OK rag torch", torch.__version__)
