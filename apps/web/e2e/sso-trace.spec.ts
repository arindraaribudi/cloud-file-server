import { test, expect } from "@playwright/test";

test("trace OIDC SSO flow end-to-end", async ({ page }) => {
  page.on("console", (m) => console.log("[browser]", m.type(), m.text()));
  page.on("request", (r) =>
    console.log("[req]", r.method(), r.url(), JSON.stringify(r.headers()["cookie"] ?? "-")),
  );
  page.on("response", async (r) => {
    const sc = r.headers()["set-cookie"];
    const loc = r.headers()["location"];
    console.log("[res]", r.status(), r.url(), "loc=" + (loc ?? "-"), "cookie=" + (sc ?? "-"));
  });

  // Open login then click SSO
  await page.goto("http://localhost:9001/login");
  await expect(page).toHaveURL(/\/login$/);

  const ssoLink = page.getByRole("link", { name: /Continue with SSO/i });
  await expect(ssoLink).toBeVisible();
  await ssoLink.click();

  // Wait for IdP page or error
  await page.waitForLoadState("networkidle", { timeout: 10_000 }).catch(() => {});
  console.log("=== final url:", page.url());
  const title = await page.title();
  const bodyText = (await page.locator("body").innerText()).slice(0, 400);
  console.log("=== title:", title);
  console.log("=== body:", bodyText);
});
