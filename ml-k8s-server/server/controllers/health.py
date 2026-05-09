from flask import jsonify, Response, Blueprint

app = Blueprint("health", __name__)


@app.route("/health")
def health_check() -> Response:
    return jsonify({"status": "ok"})
