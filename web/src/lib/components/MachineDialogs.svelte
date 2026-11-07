<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import type { Machine } from '#lib/types.ts';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import RenameDialog from './RenameDialog.svelte';

	let {
		machine,
		renaming = $bindable(false),
		removing = $bindable(false),
		onremoved
	}: {
		machine: Machine | null;
		renaming?: boolean;
		removing?: boolean;
		onremoved?: () => void;
	} = $props();
</script>

{#if machine}
	<RenameDialog
		bind:open={renaming}
		title={t('machines.rename.title')}
		description={t('machines.rename.desc')}
		label={t('common.name')}
		initial={machine.name}
		required={t('machines.rename.required')}
		onsave={async (name) => {
			const saved = await lane.renameMachine(machine.id, name);
			toasts.show(t('machines.renamed', { name: saved }));
		}}
	/>
	<ConfirmDialog
		bind:open={removing}
		title={t('machines.remove.title', { name: machine.name })}
		body={t('machines.remove.body')}
		confirmLabel={t('machines.remove.confirm')}
		onconfirm={async () => {
			const name = machine.name;
			await lane.removeMachine(machine.id);
			toasts.show(t('machines.removed', { name }));
			onremoved?.();
		}}
	/>
{/if}
