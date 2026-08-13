import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const config = await readFile(resolve(root, 'nginx.conf'), 'utf8')
const securityHeaders = await readFile(resolve(root, 'nginx-security-headers.conf'), 'utf8')
const indexHtml = await readFile(resolve(root, 'index.html'), 'utf8')
const healthLocation = config.match(/location = \/health \{([\s\S]*?)\n  \}/)

if (!config.includes('client_max_body_size ${EFFCHAT_NGINX_MAX_BODY_BYTES};')) {
  throw new Error('Nginx upload limit must be rendered from the shared deployment ceiling')
}

if (!healthLocation?.[1].includes('include /etc/nginx/conf.d/security-headers.conf;')) {
  throw new Error('/health must include the shared security headers')
}

if (!securityHeaders.includes("script-src 'self' 'wasm-unsafe-eval'")) {
  throw new Error('Graphviz requires wasm-unsafe-eval in script-src')
}

if (/(^|\s)'unsafe-eval'(?=\s|;)/.test(securityHeaders)) {
  throw new Error('plain unsafe-eval must remain disabled')
}

if (indexHtml.includes('fonts.googleapis.com') && !securityHeaders.includes("style-src 'self' 'unsafe-inline' https://fonts.googleapis.com")) {
  throw new Error('Google Fonts styles must be allowed by CSP')
}

if (indexHtml.includes('fonts.gstatic.com') && !securityHeaders.includes("font-src 'self' data: https://fonts.gstatic.com")) {
  throw new Error('Google Fonts files must be allowed by CSP')
}

if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(indexHtml)) {
  throw new Error('index.html must not contain inline scripts blocked by CSP')
}

if (!indexHtml.includes('<script src="/theme-init.js"></script>')) {
  throw new Error('theme initialization must run from the CSP-safe same-origin script')
}
