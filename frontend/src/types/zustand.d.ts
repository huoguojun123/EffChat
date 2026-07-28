declare module "zustand" {
  export { create, useStore } from "zustand/react"
  export type { StoreApi, StateCreator } from "zustand/vanilla"
}

declare module "zustand/react" {
  import type { StateCreator, StoreApi } from "zustand/vanilla"

  type Mutate<S extends StoreApi<unknown>> = S

  type UseBoundStore<S extends StoreApi<unknown>> = {
    (): ExtractState<S>
    <U>(selector: (state: ExtractState<S>) => U): U
  } & S

  type ExtractState<S> = S extends StoreApi<infer T> ? T : never

  type Create = {
    <T>(initializer: StateCreator<T>): UseBoundStore<Mutate<StoreApi<T>>>
    <T>(): (initializer: StateCreator<T>) => UseBoundStore<Mutate<StoreApi<T>>>
  }

  export const create: Create
  export function useStore<S extends StoreApi<unknown>>(api: S): ExtractState<S>
  export function useStore<S extends StoreApi<unknown>, U>(
    api: S,
    selector: (state: ExtractState<S>) => U
  ): U
}

declare module "zustand/vanilla" {
  export type StoreMutatorIdentifier = symbol | string

  export interface StoreApi<T> {
    setState: (
      partial: T | Partial<T> | ((state: T) => T | Partial<T>),
      replace?: boolean
    ) => void
    getState: () => T
    getInitialState: () => T
    subscribe: (listener: (state: T, prevState: T) => void) => () => void
  }

  export type StateCreator<T> = (
    set: StoreApi<T>["setState"],
    get: StoreApi<T>["getState"],
    store: StoreApi<T>
  ) => T
}
