<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte'

  let { label = '加载中…' }: { label?: string } = $props()
</script>

<div class="flex items-center justify-center gap-2 py-10 text-sm text-muted-foreground" role="status">
  <LoaderCircle class="size-4 animate-spin" aria-hidden="true" />
  <span>{label}</span>
</div>
