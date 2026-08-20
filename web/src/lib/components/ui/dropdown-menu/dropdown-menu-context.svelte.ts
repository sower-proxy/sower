import { getContext, setContext } from "svelte";

const KEY = Symbol("dropdown-menu");

let uid = 0;

export class DropdownMenuState {
	open = $state(false);
	triggerEl = $state<HTMLElement | null>(null);
	readonly triggerId: string;
	readonly contentId: string;

	constructor() {
		const id = ++uid;
		this.triggerId = `dropdown-trigger-${id}`;
		this.contentId = `dropdown-content-${id}`;
	}

	setOpen(next: boolean) {
		this.open = next;
	}

	toggle() {
		this.open = !this.open;
	}

	close() {
		this.open = false;
	}
}

export function setDropdownMenu(state: DropdownMenuState) {
	setContext(KEY, state);
}

export function getDropdownMenu(): DropdownMenuState {
	const state = getContext<DropdownMenuState | undefined>(KEY);
	if (!state) {
		throw new Error("DropdownMenu components must be used within <DropdownMenu>");
	}
	return state;
}
