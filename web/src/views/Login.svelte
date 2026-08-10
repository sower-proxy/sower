<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from '$lib/api'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Input from '$lib/components/ui/input/input.svelte'
  import Label from '$lib/components/ui/label/label.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Logo from '$lib/components/Logo.svelte'
  import { CircleAlert, LoaderCircle } from 'lucide-svelte'

  let { onLogin }: { onLogin: () => void } = $props()

  let password = $state('')
  let error = $state('')
  let busy = $state(false)
  let tempPassword = $state(false)
  let passwordInput: HTMLInputElement | null = $state(null)

  onMount(() => {
    passwordInput?.focus()
    api
      .loginInfo()
      .then((info) => {
        tempPassword = info.temporaryPassword
      })
      .catch(() => {
        // login info is best-effort; the login form still works without it
      })
  })

  async function submit() {
    if (!password || busy) return
    busy = true
    error = ''
    try {
      await api.login(password)
      password = ''
      onLogin()
    } catch (e) {
      error = e instanceof Error ? e.message : 'login failed'
    } finally {
      busy = false
    }
  }
</script>

<div class="flex min-h-svh items-center justify-center p-4">
  <Card.Card class="w-full max-w-sm">
    <Card.CardHeader>
      <div class="mb-1 flex size-10 items-center justify-center rounded-lg bg-primary/10">
        <Logo class="size-6" />
      </div>
      <Card.CardTitle>Sower Admin</Card.CardTitle>
      <Card.CardDescription>输入 sower 配置中的 admin 密码。</Card.CardDescription>
    </Card.CardHeader>
    <Card.CardContent>
      {#if tempPassword}
        <Alert.Alert class="mb-4">
          <CircleAlert class="size-4" />
          <Alert.AlertDescription>
            未配置固定密码，请使用 sower 启动日志中打印的临时密码登录（每次重启会更换；已登录的浏览器不受影响）。
          </Alert.AlertDescription>
        </Alert.Alert>
      {/if}
      <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); submit() }}>
        <div class="grid gap-2">
          <Label for="admin-password">密码</Label>
          <Input
            id="admin-password"
            type="password"
            bind:ref={passwordInput}
            bind:value={password}
            placeholder="admin 密码"
            autocomplete="current-password"
          />
        </div>
        {#if error}
          <Alert.Alert variant="destructive">
            <CircleAlert class="size-4" />
            <Alert.AlertDescription>{error}</Alert.AlertDescription>
          </Alert.Alert>
        {/if}
        <Button type="submit" disabled={busy || !password}>
          {#if busy}
            <LoaderCircle class="size-4 animate-spin" aria-hidden="true" />
            登录中…
          {:else}
            登录
          {/if}
        </Button>
      </form>
    </Card.CardContent>
  </Card.Card>
</div>
