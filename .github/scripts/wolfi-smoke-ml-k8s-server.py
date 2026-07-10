# ml-k8s-server native/ML deps. Importing these triggers their real dlopens on
# Wolfi; referencing each (via the modules list) both confirms the module object
# loaded and keeps linters happy (the import IS the smoke test).
import numpy
import pandas
import sklearn
import psycopg2
import tensorflow as tf

modules = [numpy, pandas, sklearn, psycopg2, tf]
print("ML_IMPORTS_OK ml-k8s:",
      ", ".join(m.__name__ for m in modules),
      "| tensorflow", tf.__version__)
