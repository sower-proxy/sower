import { mount } from "svelte";
import "./app.css";
import App from "./App.svelte";

const target = document.querySelector<HTMLDivElement>("#app");
if (!target) {
	throw new Error("missing #app mount point");
}

const app = mount(App, { target });

export default app;
