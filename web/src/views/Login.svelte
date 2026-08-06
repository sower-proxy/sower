<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from '$lib/api'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Input from '$lib/components/ui/input/input.svelte'
  import Label from '$lib/components/ui/label/label.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import { CircleAlert, LoaderCircle, Sprout } from 'lucide-svelte'

  let { onLogin }: { onLogin: () => void } = $props()

  let password = $state('')
  let error = $state('')
  let busy = $state(false)
  let passwordInput: HTMLInputElement | null = $state(null)

  onMount(() => {
    passwordInput?.focus()
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
      <div class="mb-1 flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
        <Sprout class="size-5" aria-hidden="true" />
      </div>
      <Card.CardTitle>Sower Admin</Card.CardTitle>
      <Card.CardDescription>输入 sower 配置中的 admin 密码。</Card.CardDescription>
    </Card.CardHeader>
    <Card.CardContent>
      <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); submit() }}>
        <div class="grid gap-2">
          <Label for="admin-password">Password</Label>
          <Input
            id="admin-password"
            type="password"
            bind:ref={passwordInput}
            bind:value={password}
            placeholder="Password"
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
            Signing in…
          {:else}
            Sign in
          {/if}
        </Button>
      </form>
    </Card.CardContent>
  </Card.Card>
</div>
