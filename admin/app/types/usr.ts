export interface UsrMain {
  id: string
  created_at: string
  modified_at: string
  active: boolean
  is_admin: boolean
  name: string
  username: string
}

export interface UsrCreate {
  active?: boolean
  is_admin?: boolean
  name?: string
  username?: string
  password?: string
}

export type UsrUpdate = UsrCreate

export interface TokenPair {
  jwt: string
  refresh_token: string
}
