<script lang="ts">
	import { Alert, Button, Heading } from '@runwisp/ui';

	let { expired = false, onSignIn }: { expired?: boolean; onSignIn: () => void } = $props();
</script>

<main class="grid min-h-screen place-items-center bg-surface px-4 py-10">
	<section class="w-full max-w-md space-y-5 border-t border-outline bg-surface-raised p-6 sm:p-8">
		<div class="flex items-center gap-3">
			<span class="grid h-10 w-10 place-items-center rounded-[3px] bg-primary font-mono text-lg font-extrabold text-on-primary">p</span>
			<Heading level={1} size="xl">pgkeeper</Heading>
		</div>
		<p class="text-sm leading-6 text-on-surface-muted">PostgreSQL backups, scheduled and protected.</p>
		{#if expired}
			<Alert variant="warning" title="Your session expired">
				Sign in again to continue.
			</Alert>
		{/if}
		<Button variant="primary" fullWidth onclick={onSignIn}>Sign in</Button>
	</section>
</main>
