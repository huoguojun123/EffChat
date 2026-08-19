import { expect, test, type Page, type Route } from "@playwright/test"

const timestamp = "2026-08-05T00:00:00Z"
const promptA = prompt(1, "Prompt A", "Content A")
const promptB = prompt(2, "Prompt B", "Content B")

function prompt(id: number, title: string, content: string) {
  return {
    id,
    title,
    content,
    description: `${title} description`,
    tags: [],
    group_id: null,
    group_name: "默认分组",
    is_public: true,
    created_at: timestamp,
    updated_at: timestamp,
  }
}

async function fulfillJSON(route: Route, json: unknown, delay = 0) {
  if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
  await route.fulfill({ json })
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return fulfillJSON(route, { id: 1, username: "admin", role: "admin", is_active: true })
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/admin/prompts" && request.method() === "GET") {
      return fulfillJSON(route, { prompts: [promptA, promptB], total: 2 })
    }
    if (path === `/api/v1/admin/prompts/${promptA.id}` && request.method() === "PATCH") {
      return fulfillJSON(route, { ...promptA, ...request.postDataJSON() }, 300)
    }
    if (path === `/api/v1/admin/prompts/${promptB.id}` && request.method() === "DELETE") {
      return fulfillJSON(route, { message: "deleted" }, 300)
    }
    return fulfillJSON(route, {})
  })
}

test("prompt save and delete callbacks stay with their editor generation", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/prompts")

  await page.getByText("Prompt A", { exact: true }).click()
  const content = page.getByLabel("内容")
  await content.fill("Prompt A saved revision")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await content.fill("Prompt A newer revision")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(content).toHaveValue("Prompt A newer revision")

  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByText("Prompt B", { exact: true }).click()
  await page.getByRole("dialog", { name: "放弃未保存的修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(content).toHaveValue("Content B")
  await page.waitForTimeout(350)
  await expect(content).toHaveValue("Content B")

  await page.getByRole("button", { name: "删除", exact: true }).click()
  await page.getByRole("dialog", { name: /删除提示词/ }).getByRole("button", { name: "删除提示词" }).click()
  await page.getByText("Prompt A", { exact: true }).click()
  await expect(content).toHaveValue("Prompt A newer revision")
  await page.waitForTimeout(350)
  await expect(content).toHaveValue("Prompt A newer revision")
})
