package agentdocs

// DocsHTML is the /docs interactive API reference (feature 111 /
// roadmap 1.2). It loads Scalar's standalone bundle from the jsdelivr
// CDN at view time — an explicit tradeoff: vendoring the ~1 MB bundle
// into a 12 MB daemon binary is not worth an operator-convenience
// page. The page itself carries no secrets and works offline only as
// far as showing this notice; the machine-readable contracts
// (/openapi.json, /openrpc.json) are fully self-hosted.
var DocsHTML = []byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>wa — API reference</title>
</head>
<body>
  <noscript>JavaScript required. Machine-readable contracts: <a href="/openapi.json">/openapi.json</a>, <a href="/openrpc.json">/openrpc.json</a>, <a href="/v1/errors">/v1/errors</a>.</noscript>
  <div id="app"></div>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  <script>
    Scalar.createApiReference('#app', { url: '/openapi.json' })
  </script>
</body>
</html>
`)
