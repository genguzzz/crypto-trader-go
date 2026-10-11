# Research image for research/altstudy and research/altbt (not deployed).
#   docker build -t ctg-research -f research/altstudy/Dockerfile research/altstudy
FROM python:3.12-slim
RUN pip install --no-cache-dir numpy==2.* pandas==2.* scipy lightgbm \
 && apt-get update && apt-get install -y --no-install-recommends libgomp1 && rm -rf /var/lib/apt/lists/*
WORKDIR /w
