<script lang="ts">
	import { cn, type WithElementRef } from "$lib/utils.js";
	import type { HTMLAttributes } from "svelte/elements";

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLDivElement>> = $props();
</script>

<div
	bind:this={ref}
	role="separator"
	data-slot="dropdown-menu-separator"
	class={cn("-mx-1 my-1 h-px bg-border", className)}
	{...restProps}
></div>
