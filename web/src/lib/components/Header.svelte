<script lang="ts">
	import { Button, Dropdown, ThemeToggle } from '@runwisp/ui';

	interface Props {
		user: { name?: string; email?: string; subject?: string } | null;
		authEnabled: boolean;
		onMenu: () => void;
		onLogout: () => void;
	}

	let { user, authEnabled, onMenu, onLogout }: Props = $props();
	const displayName = $derived(user?.name || user?.email || user?.subject || 'Account');
	const menuItems = $derived([{ label: 'Log out', danger: true, onClick: onLogout }]);
</script>

<header class="shell-header flex items-center justify-between border-b border-outline bg-surface-raised/95 px-3 shadow-sm md:px-5">
	<div class="flex items-center gap-2">
		<Button
			variant="ghost"
			size="sm"
			class="md:hidden"
			aria-label="Open navigation"
			onclick={onMenu}
		>
			☰
		</Button>
		<a href="/" class="flex items-center gap-2 font-mono font-semibold tracking-tight text-on-surface">
			<span class="grid h-7 w-7 place-items-center rounded-[3px] bg-primary font-extrabold text-on-primary">p</span>
			<span>pgkeeper</span>
		</a>
	</div>

	<div class="flex items-center gap-1">
		{#if authEnabled}
			<span class="hidden max-w-48 truncate pr-1 font-mono text-xs text-on-surface-muted sm:inline">
				{displayName}
			</span>
			<Dropdown items={menuItems} triggerLabel="Account menu">
				{#snippet trigger()}
					<span class="font-mono text-xs" aria-hidden="true">•••</span>
				{/snippet}
			</Dropdown>
		{/if}
		<ThemeToggle />
	</div>
</header>
