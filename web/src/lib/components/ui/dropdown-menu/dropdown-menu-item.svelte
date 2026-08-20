<script lang="ts">
	import type { Snippet } from "svelte";
	import type { HTMLButtonAttributes } from "svelte/elements";
	import { cn } from "$lib/utils.js";
	import { getDropdownMenu } from "./dropdown-menu-context.svelte.ts";

	let {
		ref = $bindable(null),
		class: className,
		inset,
		variant = "default",
		onSelect,
		onclick,
		children,
		...restProps
	}: HTMLButtonAttributes & {
		ref?: HTMLButtonElement | null;
		inset?: boolean;
		variant?: "default" | "destructive";
		onSelect?: (event: Event) => void;
		children?: Snippet;
	} = $props();

	const menu = getDropdownMenu();

	function handleSelect(event: MouseEvent & { currentTarget: EventTarget & HTMLButtonElement }) {
		onclick?.(event);
		if (event.defaultPrevented) return;
		onSelect?.(event);
		if (event.defaultPrevented) return;
		menu.close();
		menu.triggerEl?.focus();
	}
</script>

<button
	bind:this={ref}
	{...restProps}
	type="button"
	role="menuitem"
	tabindex={-1}
	data-slot="dropdown-menu-item"
	data-inset={inset}
	data-variant={variant}
	class={cn(
		"gap-1.5 rounded-md px-1.5 py-1 text-sm focus:bg-accent focus:text-accent-foreground not-data-[variant=destructive]:focus:**:text-accent-foreground data-inset:pl-7 data-[variant=destructive]:text-destructive data-[variant=destructive]:focus:bg-destructive/10 data-[variant=destructive]:focus:text-destructive dark:data-[variant=destructive]:focus:bg-destructive/20 [&_svg:not([class*='size-'])]:size-4 data-[variant=destructive]:*:[svg]:text-destructive group/dropdown-menu-item relative flex cursor-default items-center outline-hidden select-none data-[inset]:pl-8 data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
		className,
	)}
	onclick={handleSelect}
>
	{@render children?.()}
</button>
