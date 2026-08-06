// startPolling runs fn immediately, then every intervalMs while the page is
// visible, and once more when the page becomes visible again. Returns a stop
// function. Skipping hidden tabs avoids pointless admin API requests.
export function startPolling(fn: () => void, intervalMs: number): () => void {
  void fn()
  const timer = setInterval(() => {
    if (document.hidden) return
    void fn()
  }, intervalMs)

  const onVisible = () => {
    if (!document.hidden) void fn()
  }
  document.addEventListener('visibilitychange', onVisible)

  return () => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', onVisible)
  }
}
