import { expect, test, type Page, type Route } from "@playwright/test"

const skillA = skill("skill-a", "Skill A")
const skillB = skill("skill-b", "Skill B")

function skill(id: string, name: string) {
  return {
    id,
    name,
    description: `${name} description`,
    source_type: "manual",
    checksum: `${id}-checksum`,
    package_checksum: `${id}-package`,
    entry_path: "SKILL.md",
    min_group_level: 0,
    files: [{ path: "SKILL.md", kind: "entry", size: 20, checksum: `${id}-file` }],
    enabled: true,
    is_builtin: false,
    authorized: true,
    created_at: "2026-08-05T00:00:00Z",
    updated_at: "2026-08-05T00:00:00Z",
  }
}

async function fulfillJSON(route: Route, json: unknown, delay = 0) {
  if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
  await route.fulfill({ json })
}

async function installRoutes(page: Page, batchRequests?: unknown[]) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    if (path === "/api/v1/users/me") return fulfillJSON(route, { id: 1, username: "admin", role: "admin", is_active: true })
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/admin/groups") return fulfillJSON(route, { groups: [], total: 0 })
    if (path === "/api/v1/admin/skills" && request.method() === "GET") return fulfillJSON(route, { skills: [skillA, skillB] })
    if (path.endsWith("/files/content")) {
      const current = path.includes("skill-a") ? skillA : skillB
      return fulfillJSON(route, {
        file: current.files[0],
        content: `# Entry for ${current.id}`,
      }, current.id === "skill-a" ? 350 : 40)
    }
    if (path === "/api/v1/admin/skills/skill-b" && request.method() === "PATCH") {
      const payload = request.postDataJSON()
      return fulfillJSON(route, { ...skillB, ...payload, name: "Skill B (saved)" }, 300)
    }
    if (path === "/api/v1/admin/skills/import/git/preview") {
      if (batchRequests) {
        return fulfillJSON(route, {
          branches: ["main"],
          selected_ref: "main",
          skills: [
            {
              id: "upstream-a",
              name: "Skill A Updated",
              description: "updated",
              source_path: "upstream/a/SKILL.md",
              checksum: "updated-a",
              files: [{ path: "SKILL.md", kind: "entry", size: 30, checksum: "updated-a-file" }],
              existing_skill: skillA,
              match_type: "name",
              default_action: "update",
            },
            {
              id: "skill-new",
              name: "Skill New",
              description: "new",
              source_path: "upstream/new/SKILL.md",
              checksum: "new",
              files: [{ path: "SKILL.md", kind: "entry", size: 24, checksum: "new-file" }],
              default_action: "create",
            },
          ],
          report: { imported: 2 },
        })
      }
      return fulfillJSON(route, {
        branches: ["main"],
        selected_ref: "main",
        skills: [{ id: "git-candidate", name: "Git Candidate", description: "", source_path: "git", checksum: "git" }],
        report: { imported: 1 },
      }, 350)
    }
    if (path === "/api/v1/admin/skills/import/git" && request.method() === "POST") {
      batchRequests?.push(request.postDataJSON())
      return fulfillJSON(route, {
        skills: [{ ...skillA, name: "Skill A Updated" }, skill("skill-new", "Skill New")],
        report: { imported: 2 },
      })
    }
    if (path === "/api/v1/admin/skills/import/zip/preview") {
      return fulfillJSON(route, {
        skills: [{ id: "zip-candidate", name: "Zip Candidate", description: "", source_path: "zip", checksum: "zip" }],
        report: { imported: 1 },
      }, 40)
    }
    return fulfillJSON(route, {})
  })
}

test("late loads and saves cannot replace the current Skill draft", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/skills")
  await page.waitForLoadState("networkidle")

  await page.getByText("Skill A", { exact: true }).click()
  await page.getByText("Skill B", { exact: true }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Skill B")
  await page.waitForTimeout(450)
  await expect(page.getByLabel("名称")).toHaveValue("Skill B")

  await page.getByLabel("描述").fill("saved revision")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByLabel("描述").fill("newer unsaved revision")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(page.getByLabel("描述")).toHaveValue("newer unsaved revision")
  await expect(page.getByRole("button", { name: "保存", exact: true })).toBeEnabled()

  await page.getByText("Skill A", { exact: true }).click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "继续编辑" }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Skill B")
  await page.getByText("Skill A", { exact: true }).click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Skill A")
})

test("a newer Zip preview owns the dialog over a late Git preview", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/skills")
  await page.waitForLoadState("networkidle")

  await page.getByPlaceholder("Git 仓库地址").fill("https://example.invalid/skills.git")
  await page.getByRole("button", { name: "扫描 Git" }).click()
  await page.locator('input[type="file"][accept^=".zip"]').first().setInputFiles({
    name: "fictional-skills.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("fictional zip fixture"),
  })

  await expect(page.getByText("选择 Zip Skills", { exact: true })).toBeVisible()
  await expect(page.getByText("Zip Candidate", { exact: true })).toBeVisible()
  await page.waitForTimeout(450)
  await expect(page.getByText("选择 Zip Skills", { exact: true })).toBeVisible()
  await expect(page.getByText("Git Candidate", { exact: true })).toHaveCount(0)
})

test("a committed save still refreshes the catalog after the editor changes", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/skills")
  await page.waitForLoadState("networkidle")

  await page.getByText("Skill B", { exact: true }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Skill B")
  await page.getByLabel("描述").fill("committed while navigating")
  await page.getByRole("button", { name: "保存", exact: true }).click()

  await page.getByText("Skill A", { exact: true }).click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(page.getByText("Skill B (saved)", { exact: true })).toBeVisible()
  await expect(page.getByLabel("名称")).toHaveValue("Skill A")
})

test("mixed duplicate updates and creates use one atomic import request", async ({ page }) => {
  const requests: unknown[] = []
  await installRoutes(page, requests)
  await page.goto("/admin/skills")
  await page.waitForLoadState("networkidle")

  await page.getByPlaceholder("Git 仓库地址").fill("https://example.invalid/skills.git")
  await page.getByRole("button", { name: "扫描 Git" }).click()
  await expect(page.getByText("选择 Git Skills", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "导入选中项" }).click()
  await expect(page.getByText("确认重复导入", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "确认覆盖" }).click()

  await expect.poll(() => requests.length).toBe(1)
  expect(requests[0]).toEqual(expect.objectContaining({
    selected_paths: ["upstream/a/SKILL.md", "upstream/new/SKILL.md"],
    target_skill_ids: { "upstream/a/SKILL.md": "skill-a" },
  }))
  await expect(page.getByText("Skill A Updated", { exact: true })).toBeVisible()
  await expect(page.getByText("Skill New", { exact: true })).toBeVisible()
})
