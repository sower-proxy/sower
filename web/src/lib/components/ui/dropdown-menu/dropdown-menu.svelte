<script lang="ts">
	import type { Snippet } from "svelte";
	import { DropdownMenuState, setDropdownMenu } from "./dropdown-menu-context.svelte.ts";

	let { open = $bindable(false), children }: { open?: boolean; children?: Snippet } = $props();

	const menu = new DropdownMenuState();
	menu.open = open;
	setDropdownMenu(menu);

	// Outbound only: internal open drives the bindable. Callers do not pass bind:open.
	$effect(() => {
		open = menu.open;
	});
</script>

{@render children?.()}
