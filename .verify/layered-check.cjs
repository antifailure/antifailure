const puppeteer = require("/tmp/pcore/node_modules/puppeteer-core");
const path = require("path");

const out = path.join(__dirname);

async function shotSection(page, id, file) {
  await page.evaluate((elId) => {
    document.getElementById(elId)?.scrollIntoView({ block: "start" });
    window.scrollBy(0, -72);
  }, id);
  await new Promise((r) => setTimeout(r, 2200));
  const el = await page.$(`#${id}`);
  if (!el) throw new Error(`missing #${id}`);
  await el.screenshot({ path: path.join(out, file) });
  console.log(file);
}

(async () => {
  const browser = await puppeteer.launch({
    executablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    headless: "new",
    defaultViewport: { width: 1440, height: 980, deviceScaleFactor: 1 },
  });
  const page = await browser.newPage();

  await page.goto("http://127.0.0.1:3000/#migrations", { waitUntil: "networkidle0", timeout: 60000 });
  await shotSection(page, "migrations", "layered-home-migrations.png");

  await page.evaluate(() => {
    [...document.querySelectorAll("button")].find((b) =>
      (b.textContent || "").includes("Safer expand-and-contract"),
    )?.click();
  });
  await new Promise((r) => setTimeout(r, 1800));
  const mig = await page.$("#migrations");
  await mig.screenshot({ path: path.join(out, "layered-home-migrations-pass.png") });
  console.log("layered-home-migrations-pass.png");

  await shotSection(page, "twins", "layered-home-twins.png");

  await page.goto("http://127.0.0.1:3000/product/migrations", {
    waitUntil: "networkidle0",
    timeout: 60000,
  });
  await new Promise((r) => setTimeout(r, 1800));
  await page.screenshot({ path: path.join(out, "layered-product-migrations.png"), fullPage: false });
  console.log("layered-product-migrations.png");
  const productCopy = await page.evaluate(() => document.body.innerText.slice(0, 2500));
  console.log("--- product/migrations text sample ---");
  console.log(productCopy.slice(0, 800));

  await page.goto("http://127.0.0.1:3000/product/twins", {
    waitUntil: "networkidle0",
    timeout: 60000,
  });
  await new Promise((r) => setTimeout(r, 1800));
  await page.screenshot({ path: path.join(out, "layered-product-twins.png"), fullPage: false });
  console.log("layered-product-twins.png");

  await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 2 });
  await page.goto("http://127.0.0.1:3000/#migrations", { waitUntil: "networkidle0", timeout: 60000 });
  await shotSection(page, "migrations", "layered-home-migrations-mobile.png");

  const homeText = await page.evaluate(() => {
    const sec = document.getElementById("migrations");
    return sec ? sec.innerText : "";
  });
  console.log("--- #migrations labels ---");
  console.log(homeText.slice(0, 1200));

  await browser.close();
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
