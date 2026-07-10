# ml-k8s-server native/ML deps — importing these triggers their real dlopens.
import numpy, pandas, sklearn, psycopg2  # noqa: F401
import tensorflow as tf  # noqa: F401
print("ML_IMPORTS_OK ml-k8s tensorflow", tf.__version__)
