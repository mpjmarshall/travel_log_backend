# Vendored assets

Served from the app so the panel works offline and the CSP can forbid
third-party script. Nothing here is a Go dependency.

| file | source | version | sha256 |
|---|---|---|---|
| `htmx.min.js` | https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js | 2.0.4 | `e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447` |
| `PlayfairDisplay-Variable.ttf` | the client repository's `assets/fonts/` | as shipped in the app | — |

`OFL-PlayfairDisplay.txt` ships because the licence requires it to travel
with the font. Re-record the digest on any upgrade.
