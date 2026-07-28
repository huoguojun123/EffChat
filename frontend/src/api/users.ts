import { api } from "./client"
import type { User } from "@/types"

export const usersApi = {
  getMe(): Promise<User> {
    return api.get("/users/me")
  },

  updateMe(data: { nickname?: string; email?: string }): Promise<User> {
    return api.patch("/users/me", data)
  },

  uploadAvatar(file: File): Promise<User> {
    const form = new FormData()
    form.append("file", file)
    return api.upload("/users/me/avatar", form)
  },

  deleteAvatar(): Promise<User> {
    return api.delete("/users/me/avatar")
  },

  changePassword(data: { old_password: string; new_password: string }): Promise<void> {
    return api.put("/users/me/password", data)
  },
}
