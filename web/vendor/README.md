# Vendored scripts for aether-pay-desktop.html

The wallet page loads these from here instead of a CDN, so it runs no
third-party code at runtime and works offline. Unmodified copies of the
npm packages below (fetched with `npm pack`, which checks the registry's
integrity hash):

| File | Package | From |
| --- | --- | --- |
| `react-18.2.0.production.min.js` | react@18.2.0 | `umd/react.production.min.js` |
| `react-dom-18.2.0.production.min.js` | react-dom@18.2.0 | `umd/react-dom.production.min.js` |
| `babel-standalone-7.23.5.min.js` | @babel/standalone@7.23.5 | `babel.min.js` |

Licenses: MIT, in `LICENSE.react` (react and react-dom) and `LICENSE.babel`.

SHA-256:

    558a1f7f5ffe218499dcfe5e4fe28d8c890553131b2f718fd4c547be2e09c484  babel-standalone-7.23.5.min.js
    4b4969fa4ef3594324da2c6d78ce8766fbbc2fd121fff395aedf997db0a99a06  react-18.2.0.production.min.js
    21758ed084cd0e37e735722ee4f3957ea960628a29dfa6c3ce1a1d47a2d6e4f7  react-dom-18.2.0.production.min.js

To upgrade one, `npm pack <package>@<version>`, copy the file above
under a new versioned name, update the `<script>` tag and this file.
