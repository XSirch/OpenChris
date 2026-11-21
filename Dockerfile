FROM node:22-bookworm-slim AS codex
ARG CODEX_VERSION=0.163.0
RUN npm install --global --omit=dev @openai/codex@${CODEX_VERSION}

FROM python:3.13-slim-bookworm AS web
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt \
 && useradd --create-home --uid 10001 openchris \
 && install -d -o openchris -g openchris -m 0700 /var/lib/openchris /var/lib/codex
COPY openchris/ openchris/
USER openchris
ENV OPENCHRIS_STATE=/var/lib/openchris
EXPOSE 8000
CMD ["python", "-m", "openchris", "web"]

FROM web AS worker
COPY --from=codex /usr/local/bin/node /usr/local/bin/node
COPY --from=codex /usr/local/lib/node_modules/@openai/codex /usr/local/lib/node_modules/@openai/codex
USER root
RUN ln -s /usr/local/lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex \
 && codex --version
USER openchris
ENV CODEX_HOME=/var/lib/codex
CMD ["python", "-m", "openchris", "worker"]
