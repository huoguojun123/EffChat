FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
  PYTHONUNBUFFERED=1 \
  EXTRACTOR_HOST=0.0.0.0 \
  EXTRACTOR_PORT=8090

WORKDIR /app

COPY py-extractor/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY py-extractor/app ./app
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/

EXPOSE 8090

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health', timeout=2).read()" || exit 1

CMD ["sh", "-c", "uvicorn app.main:app --host ${EXTRACTOR_HOST} --port ${EXTRACTOR_PORT} --workers 1"]
