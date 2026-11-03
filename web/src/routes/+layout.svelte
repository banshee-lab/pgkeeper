<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { Alert, Button, Heading, ToastContainer } from '@runwisp/ui';
	import { toast } from '@runwisp/ui';
	import { activities } from '$lib/stores/activities.svelte';
	import { backups } from '$lib/stores/backups.svelte';
	import { clock } from '$lib/stores/clock.svelte';
	import { connection } from '$lib/stores/connection.svelte';
	import { session } from '$lib/stores/session.svelte';
	import AppShell from '$lib/components/AppShell.svelte';
	import LoginScreen from '$lib/components/LoginScreen.svelte';
	import favicon from '#lib/assets/favicon.svg';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	async function loadDashboardData() {
		await Promise.all([backups.load(), activities.load()]);
	}

	async function bootstrap() {
		await session.load();
		if (session.status === 'ready') {
			await loadDashboardData();
			connection.connect();
		}
	}

	async function logout() {
		connection.close();
		try {
			await session.logout();
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Could not log out.');
		}
	}

	onMount(() => {
		clock.start();
		void bootstrap();
		return clock.stop;
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

<ToastContainer />

{#if session.status === 'loading'}
	<main class="grid min-h-screen place-items-center bg-surface px-4">
		<p class="font-mono text-sm text-on-surface-muted" aria-live="polite">Checking session…</p>
	</main>
{:else if session.status === 'unauthenticated' || session.status === 'expired'}
	<LoginScreen
		expired={session.status === 'expired'}
		onSignIn={() => session.login(`${window.location.pathname}${window.location.search}`)}
	/>
{:else if session.status === 'error'}
	<main class="grid min-h-screen place-items-center bg-surface px-4 py-8">
		<section class="w-full max-w-lg space-y-4">
			<Heading level={1} size="xl">Unable to connect</Heading>
			<Alert variant="danger" title="Session check failed">{session.error}</Alert>
			<Button onclick={() => bootstrap()}>Retry</Button>
		</section>
	</main>
{:else}
	<AppShell user={session.user} authEnabled={session.authEnabled} onLogout={logout}>
		{@render children()}
	</AppShell>
{/if}
