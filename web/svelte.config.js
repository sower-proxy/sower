import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'
import { fileURLToPath } from 'node:url'

export default {
  preprocess: vitePreprocess(),
  vite: {
    resolve: {
      alias: {
        $lib: fileURLToPath(new URL('./src/lib', import.meta.url)),
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
  },
}
