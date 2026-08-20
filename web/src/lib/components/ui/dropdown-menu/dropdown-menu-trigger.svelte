<script lang="ts">
	import type { Snippet } from "svelte";
	import type { HTMLButtonAttributes } from "svelte/elements";
	import { getDropdownMenu } from "./dropdown-menu-context.svelte.ts";

	let {
		ref = $bindable(null),
		children,
		onclick,
		onkeydown,
		...restProps
	}: HTMLButtonAttributes & { ref?: HTMLButtonElement | null; children?: Snippet } = $props();

	const menu = getDropdownMenu();

	$effect(() => {
		menu.triggerEl = ref;
		return () => {
			if (menu.triggerEl === ref) menu.triggerEl = null;
		};
	});

	function handleClick(event: MouseEvent & { currentTarget: EventTarget & HTMLButtonElement }) {
		onclick?.(event);
		if (event.defaultPrevented) return;
		menu.toggle();
	}

	function handleKeydown(event: KeyboardEvent & { currentTarget: EventTarget & HTMLButtonElement }) {
		onkeydown?.(event);
		if (event.defaultPrevented) return;
		if (event.key === "ArrowDown") {
			event.preventDefault();
			menu.setOpen(true);
		}
	}
</script>

<button
	bind:this={ref}
	{...restProps}
	type="button"
	id={menu.triggerId}
	data-slot="dropdown-menu-trigger"
	aria-haspopup="menu"
	aria-expanded={menu.open}
	aria-controls={menu.contentId}
	onclick={handleClick}
	onkeydown={handleKeydown}
>
	{@render children?.()}
</button>
