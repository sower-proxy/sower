<script lang="ts">
  // Brand logo: a sprouting seed — sower = 播种者. Identical to
  // public/favicon.svg so the header, login page and browser tab share one
  // visual identity. The gradient id is unique per instance to avoid
  // collisions when several logos render on one page.
  let { class: className = '' }: { class?: string } = $props()
  const gradientId = $props.id()
</script>

<svg viewBox="0 0 32 32" class={className} aria-hidden="true">
  <defs>
    <linearGradient id={gradientId} x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#0d9488" />
      <stop offset="100%" stop-color="#0f766e" />
    </linearGradient>
  </defs>
  <rect width="32" height="32" rx="7" fill={`url(#${gradientId})`} />
  <g fill="none" stroke="#f5f2e6" stroke-linecap="round" stroke-linejoin="round">
    <circle cx="16" cy="24" r="3.5" fill="#f5f2e6" stroke="none" />
    <path d="M16 20.5 C16 15, 16 12, 16 8" stroke-width="3" />
    <path d="M16 12 L9.5 5.5" stroke-width="2.5" />
    <path d="M16 12 L22.5 5.5" stroke-width="2.5" />
  </g>
</svg>
