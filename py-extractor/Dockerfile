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
COPY scripts/licenses /tmp/license-tools
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /usr/share/licenses/effchat/
RUN python /tmp/license-tools/collect-third-party-licenses.py collect python \
    --root /app \
    --output /usr/share/licenses/effchat/third-party/python \
  && rm -rf /tmp/license-tools \
  && groupadd --gid 10001 extractor \
  && useradd --uid 10001 --gid 10001 --no-create-home \
    --home-dir /nonexistent --shell /usr/sbin/nologin extractor

EXPOSE 8090

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8090/health', timeout=2).read()" || exit 1

# Application and dependency layers stay root-owned and read-only to the
# runtime process. Parsers only need the image's standard 1777 /tmp for their
# bounded temporary files, so uploaded documents never require a writable app
# directory or a host mount.
USER 10001:10001

CMD ["sh", "-c", "uvicorn app.main:app --host ${EXTRACTOR_HOST} --port ${EXTRACTOR_PORT} --workers 1"]
