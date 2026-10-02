# Strategy runner (supervisor + one process per instance). Build context: strategies/.
# Runs as uid 65532 with a read-only root filesystem and no network; the only I/O is the Unix socket
# in $CTG_RUN_DIR shared with the ctg container.
FROM python:3.12-slim

RUN pip install --no-cache-dir "numpy>=2.0,<3"

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    OPENBLAS_NUM_THREADS=1 \
    OMP_NUM_THREADS=1 \
    CTG_RUN_DIR=/run/ctg

WORKDIR /app
COPY --chown=0:0 . /app

USER 65532:65532
CMD ["python", "-u", "runner.py"]
