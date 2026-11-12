# LionSQL Instant Web App Examples (`examples/README.md`)

> - **Created**: 2026-11-12
> - **Language**: English ([한국어](README.ko.md))

This folder provides ready-to-run frontend web applications designed to be hosted directly by LionSQL's built-in web server via the `--app` command-line flag.

---

## Example 1: Single-File Dashboard (`examples/single-file-dashboard`)

A self-contained HTML dashboard with Tailwind CSS CDN and LionSQL REST client integration in one file.

### How to Run:
```bash
./bin/lionsql server --app=./examples/single-file-dashboard/index.html
```

Visit `http://localhost:8090/` in your browser. All route requests are answered by the single HTML file, communicating directly with LionSQL APIs (`/api/query`, `/api/version`) on the same origin.

---

## Example 2: Flat Directory SPA App (`examples/flat-spa-app`)

A multi-file Single Page Application (SPA) demonstrating HTML5 hash routing, live table introspection, and SQL query execution.

### Structure:
- `index.html`: Entry HTML document
- `app.js`: Client-side hash routing and async query logic
- `style.css`: Clean, dark-mode CSS styles

### How to Run:
```bash
./bin/lionsql server --app=./examples/flat-spa-app
```

Visit `http://localhost:8090/` in your browser. Asset requests (`style.css`, `app.js`) are served directly from the folder, while navigation routes fall back automatically to `index.html`.
