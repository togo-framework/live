import { test, expect } from "@playwright/test";

// End-to-end through the real chat board served at /live by the live plugin.
// Selectors mirror live/web/board.html: #convs .conv (sidebar), #box (textarea),
// #send (form), .msg.agent (agent bubble).
test("chat board: prompt gets an echoed agent reply over the WS", async ({ page }) => {
  await page.goto("/live");

  // The seeded "Demo chat" conversation appears in the sidebar; click it.
  const demoChat = page.locator("#convs .conv", { hasText: "Demo chat" });
  await expect(demoChat).toBeVisible();
  await demoChat.click();

  // Type a message and submit with Enter.
  const box = page.locator("#box");
  await expect(box).toBeVisible();
  await box.click();
  await box.fill("Hello from Playwright");
  await box.press("Enter");

  // The user's message renders optimistically...
  await expect(page.locator(".msg.user", { hasText: "Hello from Playwright" })).toBeVisible();

  // ...and the server-driven echo responder pushes back an agent reply over the
  // WebSocket. Assert an agent bubble containing the echo signature appears.
  const agentReply = page.locator(".msg.agent", { hasText: "You said:" });
  await expect(agentReply).toBeVisible({ timeout: 15_000 });
  await expect(agentReply).toContainText("Hello from Playwright");
});
