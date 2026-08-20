<script lang="ts">
	import { tick, type Snippet } from "svelte";
	import type { HTMLAttributes } from "svelte/elements";
	import { cn } from "$lib/utils.js";
	import { getDropdownMenu } from "./dropdown-menu-context.svelte.ts";

	let {
		ref = $bindable(null),
		sideOffset = 4,
		align = "start",
		class: className,
		onOpenAutoFocus,
		onkeydown,
		children,
		...restProps
	}: HTMLAttributes<HTMLDivElement> & {
		ref?: HTMLDivElement | null;
		sideOffset?: number;
		align?: "start" | "center" | "end";
		onOpenAutoFocus?: (event: Event) => void;
		children?: Snippet;
	} = $props();

	const menu = getDropdownMenu();
	let side = $state<"top" | "bottom">("bottom");

	function portal(node: HTMLElement) {
		document.body.appendChild(node);
		return {
			destroy() {
				node.remove();
			},
		};
	}

	function place(node: HTMLElement) {
		const trigger = menu.triggerEl;
		if (!trigger) return;
		const rect = trigger.getBoundingClientRect();
		const availableBelow = window.innerHeight - rect.bottom - sideOffset - 8;
		const availableAbove = rect.top - sideOffset - 8;
		const width = node.offsetWidth;
		const fullHeight = node.scrollHeight;
		let nextSide: "top" | "bottom" = "bottom";
		let available = availableBelow;
		if (fullHeight > availableBelow && availableAbove > availableBelow) {
			nextSide = "top";
			available = availableAbove;
		}
		const height = Math.min(fullHeight, Math.max(available, 0));
		let top =
			nextSide === "bottom" ? rect.bottom + sideOffset : rect.top - sideOffset - height;
		let left = rect.left;
		if (align === "end") left = rect.right - width;
		if (align === "center") left = rect.left + (rect.width - width) / 2;
		left = Math.min(Math.max(8, left), Math.max(8, window.innerWidth - width - 8));
		node.style.position = "fixed";
		node.style.top = `${Math.max(8, top)}px`;
		node.style.left = `${left}px`;
		node.style.maxHeight = `${Math.max(available, 0)}px`;
		side = nextSide;
	}

	$effect(() => {
		if (!menu.open || !ref) return;
		const panel: HTMLDivElement = ref;
		let cancelled = false;

		place(panel);
		void tick().then(() => {
			if (cancelled || ref !== panel || !menu.open) return;
			place(panel);
			const ev = new Event("openAutoFocus", { cancelable: true });
			onOpenAutoFocus?.(ev);
			if (!ev.defaultPrevented) {
				panel.querySelector<HTMLElement>('[data-slot="dropdown-menu-item"]')?.focus();
			}
		});

		function onPointerDown(event: PointerEvent) {
			const target = event.target;
			if (!(target instanceof Node)) return;
			if (panel.contains(target) || menu.triggerEl?.contains(target)) return;
			menu.close();
		}

		function onWindowChange(event: Event) {
			if (event.type === "scroll" && event.target instanceof Node && panel.contains(event.target)) {
				return;
			}
			place(panel);
		}

		document.addEventListener("pointerdown", onPointerDown, true);
		window.addEventListener("resize", onWindowChange);
		window.addEventListener("scroll", onWindowChange, true);
		return () => {
			cancelled = true;
			document.removeEventListener("pointerdown", onPointerDown, true);
			window.removeEventListener("resize", onWindowChange);
			window.removeEventListener("scroll", onWindowChange, true);
		};
	});

	function handleKeydown(event: KeyboardEvent) {
		onkeydown?.(event as KeyboardEvent & { currentTarget: EventTarget & HTMLDivElement });
		if (event.defaultPrevented) return;
		if (event.key === "Escape") {
			event.preventDefault();
			menu.close();
			menu.triggerEl?.focus();
			return;
		}
		if (event.key === "Tab") {
			event.preventDefault();
			menu.close();
			menu.triggerEl?.focus();
			return;
		}
		if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
		const items = Array.from(
			ref?.querySelectorAll<HTMLElement>('[data-slot="dropdown-menu-item"]') ?? [],
		);
		if (items.length === 0) return;
		event.preventDefault();
		const current = items.indexOf(document.activeElement as HTMLElement);
		const fallback = event.key === "End" || event.key === "ArrowUp" ? items.length - 1 : 0;
		const active = current === -1 ? fallback : current;
		const next =
			event.key === "Home"
				? 0
				: event.key === "End"
					? items.length - 1
					: event.key === "ArrowDown"
						? (active + 1) % items.length
						: (active - 1 + items.length) % items.length;
		items[next]?.focus();
	}
</script>

{#if menu.open}
	<div
		bind:this={ref}
		use:portal
		id={menu.contentId}
		role="menu"
		tabindex="-1"
		data-slot="dropdown-menu-content"
		data-side={side}
		data-open=""
		aria-labelledby={menu.triggerId}
		{...restProps}
		style="position: fixed; top: -9999px; left: -9999px"
		class={cn(
			"z-50 min-w-32 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none duration-100 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
			className,
		)}
		onkeydown={handleKeydown}
	>
		{@render children?.()}
	</div>
{/if}
