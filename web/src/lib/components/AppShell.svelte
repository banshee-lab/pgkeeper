<script lang="ts">
	import type { Snippet } from 'svelte';
	import Header from './Header.svelte';
	import Sidebar from './Sidebar.svelte';

	interface Props {
		user: { name?: string; email?: string; subject?: string } | null;
		authEnabled: boolean;
		onLogout: () => void;
		children: Snippet;
	}

	let { user, authEnabled, onLogout, children }: Props = $props();
	let drawerOpen = $state(false);

</script>

<Header
	{user}
	{authEnabled}
	onMenu={() => (drawerOpen = true)}
	{onLogout}
/>
<Sidebar open={drawerOpen} onClose={() => (drawerOpen = false)} />
<main class="shell-main">
	<div class="mx-auto w-full max-w-[80rem] px-4 py-6 sm:px-6 lg:px-8">
		{@render children()}
	</div>
</main>
