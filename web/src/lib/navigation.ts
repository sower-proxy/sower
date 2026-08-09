import type { Category, Source } from "./api";

export type NavKey = "overview" | "rules" | "traffic" | "config";

export interface NavItem {
	key: NavKey;
	label: string;
	description: string;
}

// FavoritePage is a pinned page that may carry its sub-menu state (rules
// category, traffic source) so the shortcut lands on the exact view the
// user pinned.
export interface FavoritePage {
	key: NavKey;
	category?: Category | "miss";
	source?: Source;
}

export interface BreadcrumbContextOption {
	label: string;
	value: string;
	active?: boolean;
	description?: string;
}

export interface BreadcrumbContextSegment {
	label: string;
	value: string;
	title?: string;
	tone?: "default" | "strong" | "method";
	options?: BreadcrumbContextOption[];
	// Informational segments (e.g. the build version) yield to the nav
	// controls on narrow viewports instead of wrapping the header.
	mobileHidden?: boolean;
}

export const navItems: NavItem[] = [
	{ key: "overview", label: "概览", description: "运行状态与流量总览" },
	{ key: "rules", label: "规则", description: "运行期路由规则管理" },
	{ key: "traffic", label: "流量", description: "按域名聚合的实时流量" },
	{ key: "config", label: "配置", description: "生效配置与运行期调整" },
];
