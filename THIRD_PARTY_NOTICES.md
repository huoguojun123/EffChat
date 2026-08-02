# Third-Party Notices

EffChat's original source code is licensed under the Apache License 2.0.
Third-party software and materials remain subject to their own copyright
notices and license terms. Inclusion in this repository or in an EffChat
container image does not relicense third-party work under the EffChat license.

The authoritative dependency versions are recorded in `backend/go.mod`,
`backend/go.sum`, `frontend/package-lock.json`, and
`py-extractor/requirements.txt`. Transitive dependencies listed in those lock
files remain under their respective upstream licenses.

## Backend

| Component | License | Source |
| --- | --- | --- |
| Anthropic Go SDK | MIT | <https://github.com/anthropics/anthropic-sdk-go> |
| CloudWeGo Eino and Eino extensions | Apache-2.0 | <https://github.com/cloudwego/eino> |
| OpenAI Go SDK | Apache-2.0 | <https://github.com/openai/openai-go> |
| Local Eino Claude adapter patch (`backend/third_party/eino-claude`) | Apache-2.0 | Upstream `components/model/claude` v0.1.20; original license and patch scope retained in that directory |
| Gin and gin-contrib/cors | MIT | <https://github.com/gin-gonic/gin> |
| golang-jwt/jwt | MIT | <https://github.com/golang-jwt/jwt> |
| google/uuid | BSD-3-Clause | <https://github.com/google/uuid> |
| joho/godotenv | MIT | <https://github.com/joho/godotenv> |
| ledongthuc/pdf | BSD-3-Clause | <https://github.com/ledongthuc/pdf> |
| lib/pq | MIT | <https://github.com/lib/pq> |
| xuri/excelize | BSD-3-Clause | <https://github.com/qax-os/excelize> |
| Go supplementary libraries | BSD-3-Clause | <https://pkg.go.dev/golang.org/x> |
| Google Gen AI Go SDK | Apache-2.0 | <https://github.com/googleapis/go-genai> |

## Frontend

| Component | License | Source |
| --- | --- | --- |
| dnd-kit | MIT | <https://github.com/clauderic/dnd-kit> |
| HPCC Systems WebAssembly | Apache-2.0 | <https://github.com/hpcc-systems/hpcc-js-wasm> |
| Radix UI Primitives | MIT | <https://github.com/radix-ui/primitives> |
| Tailwind CSS and Tailwind Vite plugin | MIT | <https://github.com/tailwindlabs/tailwindcss> |
| class-variance-authority | Apache-2.0 | <https://github.com/joe-bell/cva> |
| clsx | MIT | <https://github.com/lukeed/clsx> |
| KaTeX | MIT | <https://github.com/KaTeX/KaTeX> |
| Lucide React | ISC | <https://github.com/lucide-icons/lucide> |
| Mermaid | MIT | <https://github.com/mermaid-js/mermaid> |
| React and React DOM | MIT | <https://github.com/facebook/react> |
| react-markdown and unified remark/rehype packages | MIT | <https://github.com/remarkjs/react-markdown> |
| React Router | MIT | <https://github.com/remix-run/react-router> |
| Shiki | MIT | <https://github.com/shikijs/shiki> |
| tailwind-merge | MIT | <https://github.com/dcastil/tailwind-merge> |
| tailwindcss-animate | MIT | <https://github.com/jamiebuilds/tailwindcss-animate> |
| Zustand | MIT | <https://github.com/pmndrs/zustand> |

## Python Extractor

| Component | License | Source |
| --- | --- | --- |
| FastAPI | MIT | <https://github.com/fastapi/fastapi> |
| Uvicorn | BSD-3-Clause | <https://github.com/encode/uvicorn> |
| python-multipart | Apache-2.0 | <https://github.com/Kludex/python-multipart> |
| pdfplumber | MIT | <https://github.com/jsvine/pdfplumber> |
| python-docx | MIT | <https://github.com/python-openxml/python-docx> |
| openpyxl | MIT | <https://foss.heptapod.net/openpyxl/openpyxl> |
| python-pptx | MIT | <https://github.com/scanny/python-pptx> |

## Container Images

EffChat's Docker build uses official Go, Node.js, Alpine Linux, nginx, Python,
and PostgreSQL images. These images contain software under multiple upstream
licenses. Their image documentation and included license files are the
authoritative notices for operating-system and runtime packages:

- <https://hub.docker.com/_/golang>
- <https://hub.docker.com/_/node>
- <https://hub.docker.com/_/alpine>
- <https://hub.docker.com/_/nginx>
- <https://hub.docker.com/_/python>
- <https://hub.docker.com/_/postgres>

EffChat application images include this repository's `LICENSE`, `NOTICE`, and
`THIRD_PARTY_NOTICES.md` under `/usr/share/licenses/effchat/`.

## Prompts, Fonts, Icons, and Other Materials

Non-code materials do not become Apache-2.0 merely by being included in
EffChat. Any externally sourced prompt, font, icon, image, or other material
must retain its original source, copyright notice, license, attribution, and
modification notice where required.

The default system prompt remains unchanged during open-source preparation.
Its original upstream source and license coverage must be verified before the
repository is made public. This notice does not claim that third-party prompt
content is relicensed under Apache-2.0.
