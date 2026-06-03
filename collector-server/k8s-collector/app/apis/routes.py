class Routes:
    def __init__(self, url_prefix, view):
        self.url_prefix = url_prefix
        self.view = view

    def add_namespace(self, namespace: str):
        self.url_prefix = "".join((namespace, self.url_prefix))

    def get_url_prefix(self):
        return self.url_prefix
