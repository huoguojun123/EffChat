FROM python:3.12-slim@sha256:646fb0bca3dd3ea1bcc6feb72c17ed16eed6e10cffc732fcc1478bd3e7f02d7b

ENV PYTHONDONTWRITEBYTECODE=1 \
  PYTHONUNBUFFERED=1 \
  EXTRACTOR_HOST=0.0.0.0 \
  EXTRACTOR_PORT=8090

WORKDIR /app

COPY py-extractor/requirements.lock .
# The committed lock contains the complete Python dependency graph and hashes;
# a missing or changed artifact must fail the image build instead of silently
# resolving a different transitive package set.
RUN pip install --no-cache-dir --require-hashes -r requirements.lock

COPY py-extractor/app ./app
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/

EXPOSE 8090

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health', timeout=2).read()" || exit 1

CMD ["sh", "-c", "uvicorn app.main:app --host ${EXTRACTOR_HOST} --port ${EXTRACTOR_PORT} --workers 1"]
