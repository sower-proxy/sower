import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import { cubicOut } from "svelte/easing";
import type { Snippet } from "svelte";
import type { TransitionConfig } from "svelte/transition";

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

// Helper types used by shadcn-svelte component templates.
export type WithoutChild<T> = Omit<T, "child">;

export type WithChild<T, U extends unknown[] = []> = T & {
	child: Snippet<U>;
};

export type WithChildren<T, U extends unknown[] = []> = T & {
	children: Snippet<U>;
};

export type WithoutChildrenOrChild<T> = Omit<T, "children" | "child">;

export type WithoutChildren<T> = Omit<T, "children">;

export type WithElementRef<T> = T & {
	elementRef?: unknown;
	// Bindable `ref` prop pattern used by shadcn-svelte templates for element refs.
	ref?: HTMLElement | null;
};

export const flyAndScale = (
	node: Element,
	params: { y?: number; x?: number; start?: number; duration?: number } = {},
): TransitionConfig => {
	const style = getComputedStyle(node);
	const transform = style.transform === "none" ? "" : style.transform;

	const scaleConversion = (
		valueA: number,
		scaleA: [number, number],
		scaleB: [number, number],
	) => {
		const [minA, maxA] = scaleA;
		const [minB, maxB] = scaleB;
		const percentage = (valueA - minA) / (maxA - minA);
		return percentage * (maxB - minB) + minB;
	};

	const styleToString = (
		style: Record<string, number | string | undefined>,
	): string => {
		return Object.keys(style).reduce((str, key) => {
			if (style[key] === undefined) return str;
			return str + `${key}:${style[key]};`;
		}, "");
	};

	return {
		duration: params.duration ?? 200,
		delay: 0,
		css: (t) => {
			const y = scaleConversion(t, [0, 1], [params.y ?? 5, 0]);
			const x = scaleConversion(t, [0, 1], [params.x ?? 0, 0]);
			const scale = scaleConversion(t, [0, 1], [params.start ?? 0.95, 1]);
			return styleToString({
				transform: `${transform} translate3d(${x}px, ${y}px, 0) scale(${scale})`,
				opacity: t,
			});
		},
		easing: cubicOut,
	};
};
