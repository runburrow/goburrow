# Burrow landing page

A static page: `index.html`, `styles.css`, `site.js`, and `assets/`. No build step and no
external requests (fonts, images, and scripts are all local). It follows the OS light/dark
setting, like the app.

## Before launch

- **Demo URL. Don't publish until the demo is live.** The main action, "Try the demo", links to
  `https://demo.goburrow.xyz`, which doesn't serve anything yet. Once the public demo runs,
  point all three links at it in one go:

  ```sh
  sed -i 's#https://demo.goburrow.xyz#https://YOUR-DEMO-URL#g' site/index.html
  ```
- **Domain.** The canonical and Open Graph URLs use `https://goburrow.xyz/` (3 places in the
  `<head>` of `index.html`). Update them if the domain changes.
- **Hosting.** Any static host works. For GitHub Pages, publish this `site/` folder and add a
  `CNAME` file containing the domain.

## Screenshots

`assets/shots/` holds real screenshots of the dashboard taken from a local instance seeded
with synthetic data. Each has a `.json` sidecar recording how it was captured. Retake them
after visible dashboard changes so the page doesn't drift from the product.

## Preview locally

```sh
python3 -m http.server 8090 --directory site
```
