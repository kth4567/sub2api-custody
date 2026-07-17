/**
 * Account Custody Marketplace endpoints (号主：托管账号 / 收益 / 提现)
 */

import { apiClient } from './client'

export interface OwnerEarningSummary {
  user_id: number
  earning_quota: number
  frozen_quota: number
  history_quota: number
  hosted_account_count: number
}

export interface OwnerHostedAccount {
  id: number
  name: string
  platform: string
  type: string
  status: string
  created_at: string
}

export interface OwnerWithdrawal {
  id: number
  user_id: number
  amount: number
  status: string
  method: string
  account_info: string
  review_note: string
  created_at: string
}

export interface HostAccountRequest {
  name: string
  platform: string
  type: string
  credentials: string // 凭证 JSON 字符串
}

export interface WithdrawRequest {
  amount: number
  method?: string
  account_info?: string
}

/** 号主收益汇总 */
export async function getEarnings(): Promise<OwnerEarningSummary> {
  const { data } = await apiClient.get<OwnerEarningSummary>('/user/owner/earnings')
  return data
}

/** 我的托管账号（不含凭证） */
export async function listHostedAccounts(): Promise<OwnerHostedAccount[]> {
  const { data } = await apiClient.get<OwnerHostedAccount[]>('/user/owner/accounts')
  return data ?? []
}

/** 托管一个订阅账号 */
export async function hostAccount(payload: HostAccountRequest): Promise<{ id: number }> {
  const { data } = await apiClient.post<{ id: number }>('/user/owner/accounts', payload)
  return data
}

/** 退管账号 */
export async function unhostAccount(id: number): Promise<void> {
  await apiClient.delete(`/user/owner/accounts/${id}`)
}

/** 发起提现 */
export async function requestWithdrawal(payload: WithdrawRequest): Promise<{ id: number }> {
  const { data } = await apiClient.post<{ id: number }>('/user/owner/withdrawals', payload)
  return data
}

/** 我的提现单 */
export async function listWithdrawals(limit = 20, offset = 0): Promise<OwnerWithdrawal[]> {
  const { data } = await apiClient.get<OwnerWithdrawal[]>('/user/owner/withdrawals', {
    params: { limit, offset }
  })
  return data ?? []
}

export const ownerAPI = {
  getEarnings,
  listHostedAccounts,
  hostAccount,
  unhostAccount,
  requestWithdrawal,
  listWithdrawals
}

export default ownerAPI
