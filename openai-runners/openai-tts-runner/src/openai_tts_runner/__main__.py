import os

import uvicorn

from .app import app


def main() -> None:
    port = int(os.environ.get("RUNNER_PORT", "8080"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info", workers=1)


if __name__ == "__main__":
    main()
