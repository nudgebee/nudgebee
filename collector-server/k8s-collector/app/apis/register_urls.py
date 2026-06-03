from apis.base.urls import UrlConfig
from apis.v1 import urls as v1urls

# ALL IMPORTS

URL_CONFIG = UrlConfig()
URL_CONFIG.registers_urls(v1urls.URL_CONFIG.urls)
