export function formatRetryDelay(delayMs: number) {
  if (delayMs <= 0) return "正在重新请求"
  const seconds = delayMs / 1000
  return `${Number.isInteger(seconds) ? seconds : seconds.toFixed(1)} 秒`
}
