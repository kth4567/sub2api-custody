<script setup lang="ts">
/**
 * 号主中心（账号托管市场）：托管账号 / 收益 / 提现。
 * 依赖后端 /user/owner/* 接口（见 backend internal/handler/owner_handler.go）。
 */
import { ref, onMounted } from 'vue'
import ownerAPI, {
  type OwnerEarningSummary,
  type OwnerHostedAccount,
  type OwnerWithdrawal
} from '@/api/owner'

const loading = ref(false)
const errorMsg = ref('')
const okMsg = ref('')

const summary = ref<OwnerEarningSummary | null>(null)
const accounts = ref<OwnerHostedAccount[]>([])
const withdrawals = ref<OwnerWithdrawal[]>([])

// 托管表单
const hostForm = ref({ name: '', platform: 'claude', type: 'oauth', credentials: '' })
const platforms = ['claude', 'openai', 'gemini', 'grok', 'antigravity']
const accountTypes = ['oauth', 'api_key', 'cookie']

// 提现表单
const wdForm = ref({ amount: 0, method: 'alipay', account_info: '' })
const methods = ['alipay', 'wechat', 'usdt', 'bank']

function flash(msg: string, ok = true) {
  if (ok) {
    okMsg.value = msg
    errorMsg.value = ''
  } else {
    errorMsg.value = msg
    okMsg.value = ''
  }
  setTimeout(() => {
    okMsg.value = ''
    errorMsg.value = ''
  }, 4000)
}

async function loadAll() {
  loading.value = true
  try {
    const [s, a, w] = await Promise.all([
      ownerAPI.getEarnings(),
      ownerAPI.listHostedAccounts(),
      ownerAPI.listWithdrawals()
    ])
    summary.value = s
    accounts.value = a
    withdrawals.value = w
  } catch (e: any) {
    flash(e?.message || '加载失败', false)
  } finally {
    loading.value = false
  }
}

async function submitHost() {
  if (!hostForm.value.name || !hostForm.value.credentials) {
    flash('请填写名称与凭证', false)
    return
  }
  try {
    await ownerAPI.hostAccount({ ...hostForm.value })
    flash('托管成功')
    hostForm.value.name = ''
    hostForm.value.credentials = ''
    await loadAll()
  } catch (e: any) {
    flash(e?.message || '托管失败', false)
  }
}

async function unhost(id: number) {
  if (!confirm('确认退管该账号？将停止调度并停止计入收益。')) return
  try {
    await ownerAPI.unhostAccount(id)
    flash('已退管')
    await loadAll()
  } catch (e: any) {
    flash(e?.message || '退管失败', false)
  }
}

async function submitWithdraw() {
  if (!wdForm.value.amount || wdForm.value.amount <= 0) {
    flash('请输入提现金额', false)
    return
  }
  try {
    await ownerAPI.requestWithdrawal({ ...wdForm.value })
    flash('提现申请已提交，等待审核')
    wdForm.value.amount = 0
    wdForm.value.account_info = ''
    await loadAll()
  } catch (e: any) {
    flash(e?.message || '提现失败', false)
  }
}

function money(n: number | undefined): string {
  return (n ?? 0).toFixed(4)
}

onMounted(loadAll)
</script>

<template>
  <div class="mx-auto max-w-5xl space-y-6 p-4">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">号主中心 · 账号托管市场</h1>
        <p class="text-sm text-gray-500">托管你的订阅账号，被调用时按分成获取收益并可提现。</p>
      </div>
      <button
        class="rounded-md border border-gray-300 px-3 py-1.5 text-sm hover:bg-gray-50 dark:border-gray-600 dark:hover:bg-gray-800"
        :disabled="loading"
        @click="loadAll"
      >
        刷新
      </button>
    </header>

    <p v-if="okMsg" class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">{{ okMsg }}</p>
    <p v-if="errorMsg" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ errorMsg }}</p>

    <!-- 收益概览 -->
    <section class="grid grid-cols-2 gap-4 md:grid-cols-4">
      <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <div class="text-xs text-gray-500">可提现收益</div>
        <div class="mt-1 text-lg font-semibold text-emerald-600">{{ money(summary?.earning_quota) }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <div class="text-xs text-gray-500">冻结中</div>
        <div class="mt-1 text-lg font-semibold text-amber-600">{{ money(summary?.frozen_quota) }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <div class="text-xs text-gray-500">累计收益</div>
        <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">{{ money(summary?.history_quota) }}</div>
      </div>
      <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <div class="text-xs text-gray-500">托管账号数</div>
        <div class="mt-1 text-lg font-semibold text-gray-800 dark:text-gray-200">{{ summary?.hosted_account_count ?? 0 }}</div>
      </div>
    </section>

    <!-- 托管账号 -->
    <section class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <h2 class="mb-3 text-base font-medium">托管账号</h2>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-4">
        <input v-model="hostForm.name" placeholder="账号名称" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900" />
        <select v-model="hostForm.platform" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900">
          <option v-for="p in platforms" :key="p" :value="p">{{ p }}</option>
        </select>
        <select v-model="hostForm.type" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900">
          <option v-for="t in accountTypes" :key="t" :value="t">{{ t }}</option>
        </select>
        <button class="rounded-md bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-700" @click="submitHost">托管</button>
      </div>
      <textarea
        v-model="hostForm.credentials"
        rows="3"
        placeholder='凭证 JSON，例如 {"access_token":"...","refresh_token":"..."} 或 {"api_key":"sk-..."}'
        class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-xs dark:border-gray-600 dark:bg-gray-900"
      ></textarea>
      <p class="mt-1 text-xs text-gray-400">凭证仅用于平台调度，后台建议加密存储；托管即表示你同意共享该账号额度参与拼车。</p>

      <table class="mt-4 w-full text-sm">
        <thead>
          <tr class="border-b text-left text-xs text-gray-500">
            <th class="py-2">ID</th><th>名称</th><th>平台</th><th>类型</th><th>状态</th><th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in accounts" :key="a.id" class="border-b last:border-0">
            <td class="py-2">{{ a.id }}</td>
            <td>{{ a.name }}</td>
            <td>{{ a.platform }}</td>
            <td>{{ a.type }}</td>
            <td>{{ a.status }}</td>
            <td><button class="text-red-600 hover:underline" @click="unhost(a.id)">退管</button></td>
          </tr>
          <tr v-if="accounts.length === 0"><td colspan="6" class="py-3 text-center text-gray-400">暂无托管账号</td></tr>
        </tbody>
      </table>
    </section>

    <!-- 提现 -->
    <section class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <h2 class="mb-3 text-base font-medium">提现（仅可提现收益，7 天最多 3 次）</h2>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-4">
        <input v-model.number="wdForm.amount" type="number" min="0" step="0.01" placeholder="金额" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900" />
        <select v-model="wdForm.method" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900">
          <option v-for="m in methods" :key="m" :value="m">{{ m }}</option>
        </select>
        <input v-model="wdForm.account_info" placeholder="收款账号" class="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900" />
        <button class="rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700" @click="submitWithdraw">申请提现</button>
      </div>

      <table class="mt-4 w-full text-sm">
        <thead>
          <tr class="border-b text-left text-xs text-gray-500">
            <th class="py-2">单号</th><th>金额</th><th>方式</th><th>状态</th><th>备注</th><th>时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="w in withdrawals" :key="w.id" class="border-b last:border-0">
            <td class="py-2">{{ w.id }}</td>
            <td>{{ money(w.amount) }}</td>
            <td>{{ w.method }}</td>
            <td>{{ w.status }}</td>
            <td>{{ w.review_note }}</td>
            <td>{{ w.created_at }}</td>
          </tr>
          <tr v-if="withdrawals.length === 0"><td colspan="6" class="py-3 text-center text-gray-400">暂无提现记录</td></tr>
        </tbody>
      </table>
    </section>
  </div>
</template>
