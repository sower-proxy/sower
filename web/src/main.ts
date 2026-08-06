import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

document.documentElement.classList.add('dark')

const target = document.querySelector<HTMLDivElement>('#app')
if (!target) {
  throw new Error('missing #app mount point')
}

const app = mount(App, { target })

export default app
