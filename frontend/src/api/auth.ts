import { api } from "./client"
import type { AuthResponse, RegisterResponse } from "@/types"

export function login(username: string, password: string) {
  return api.post<AuthResponse>("/auth/login", { username, password })
}

export function register(username: string, password: string) {
  return api.post<RegisterResponse>("/auth/register", { username, password })
}
