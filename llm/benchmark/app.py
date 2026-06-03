import glob
import importlib.util
import logging
import os
from pathlib import Path

import uvicorn
from dotenv import load_dotenv
from fastapi import FastAPI
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

# Load .env FIRST before any imports that use Config
load_dotenv()

from benchmark_server.config import setup_logger, get_log_config_path  # noqa: E402
from benchmark_server.middleware.auth import AuthMiddleware  # noqa: E402
from benchmark_server.utils.db_utils import init_db  # noqa: E402
from benchmark_server.utils.utils import set_global_trace  # noqa: E402

# Initialize OpenTelemetry tracing before app creation
set_global_trace()
setup_logger()

# Initialize benchmark DB tables
init_db()

logger = logging.getLogger(__name__)

# root_path tells FastAPI about the reverse-proxy prefix (e.g. /benchmark)
# so that request.url_for() generates correct absolute URLs for OAuth callbacks.
app = FastAPI(root_path=os.environ.get("ROOT_PATH", ""))

# Authentication middleware — validates session cookie (set by the
# magic-link callback) or Bearer JWT. Browser flow uses email magic link
# rendered by notifications-server; programmatic clients use RPC JWT.
app.add_middleware(AuthMiddleware)

# Serve the dashboard HTML at /
_static_dir = Path(__file__).parent / "benchmark_server" / "static"
if _static_dir.exists():

    @app.get("/")
    async def dashboard_root():
        return FileResponse(
            str(_static_dir / "index.html"),
            headers={"Cache-Control": "no-cache, no-store, must-revalidate"},
        )

    app.mount("/static", StaticFiles(directory=str(_static_dir)), name="static")

controllers_dir = os.path.join(os.path.dirname(__file__), "benchmark_server", "controllers")
controller_files = glob.glob(os.path.join(controllers_dir, "*.py"))
for file_path in controller_files:
    module_name = os.path.splitext(os.path.basename(file_path))[0]
    if module_name == "__init__":
        continue
    module_path = f"benchmark_server.controllers.{module_name}"
    try:
        module = importlib.import_module(module_path)
        if hasattr(module, "router"):
            app.include_router(module.router)
    except Exception as e:
        logger.error(f"Failed to import {module_path}: {e}")
        continue

if __name__ == "__main__":
    port = int(os.environ.get("BENCHMARK_PORT", 9999))
    reload = os.environ.get("RELOAD", "false").lower() == "true"

    # log_config=None prevents uvicorn from overriding our logging setup.
    # We handle logging via setup_logger() + logging.json above.
    uvicorn.run(
        "app:app",
        host="0.0.0.0",
        port=port,
        reload=reload,
        log_config=None,
        access_log=False,
        use_colors=False,
    )
