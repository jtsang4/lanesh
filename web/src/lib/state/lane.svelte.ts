import { api } from '../api';
import type { CreatedKey, Key, KeyKind, Lane, LaneSkill, Machine } from '../types';
import { app } from './app.svelte';
import { poll } from './poll';

export interface FreshKey {
	id: string;
	name: string;
	token: string;
}

/** Tokens created in this browser session, kept in memory only so snippets can use them. */
const revealed = new Map<string, Partial<Record<KeyKind, FreshKey>>>();

/** Data for the lane being viewed. Machines are polled so status stays live. */
class LaneState {
	id = $state('');
	lane = $state.raw<Lane | null>(null);
	error = $state.raw<unknown>(null);
	machines = $state.raw<Machine[] | null>(null);
	machinesError = $state.raw<unknown>(null);
	skills = $state.raw<LaneSkill[] | null>(null);
	skillsError = $state.raw<unknown>(null);
	keys = $state.raw<Key[] | null>(null);
	keysError = $state.raw<unknown>(null);
	fresh = $state.raw<Partial<Record<KeyKind, FreshKey>>>({});

	onlineCount = $derived(this.machines?.filter((m) => m.online).length ?? 0);
	machineNames = $derived(new Map((this.machines ?? []).map((m) => [m.id, m.name])));
	keyNames = $derived(new Map((this.keys ?? []).map((k) => [k.id, k.name])));

	#skillsSig: string | null = null;

	/** Starts loading lane `id`. Returns a cleanup function that stops polling. */
	start(id: string): () => void {
		this.id = id;
		this.lane = null;
		this.error = null;
		this.machines = null;
		this.machinesError = null;
		this.skills = null;
		this.skillsError = null;
		this.keys = null;
		this.keysError = null;
		this.#skillsSig = null;
		this.fresh = revealed.get(id) ?? {};

		void this.loadLane();
		void this.loadMachines();
		void this.loadKeys();
		return poll(() => this.loadMachines(), 5000);
	}

	async loadLane() {
		const id = this.id;
		try {
			const lane = await api.lane(id);
			if (id !== this.id) return;
			this.lane = lane;
			this.error = null;
		} catch (err) {
			if (id === this.id) this.error = err;
		}
	}

	async loadMachines() {
		const id = this.id;
		try {
			const machines = await api.machines(id);
			if (id !== this.id) return;
			machines.sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
			this.machines = machines;
			this.machinesError = null;
			if (this.lane) {
				const online = machines.filter((m) => m.online).length;
				if (this.lane.machine_count !== machines.length || this.lane.online_count !== online) {
					this.lane = { ...this.lane, machine_count: machines.length, online_count: online };
				}
			}
			// Refetch the lane-wide skill list only when a machine's skill index changed.
			const sig = machines.map((m) => `${m.id}:${m.skills_hash}:${m.online}:${m.name}`).join('|');
			if (sig !== this.#skillsSig) {
				this.#skillsSig = sig;
				void this.loadSkills();
			}
		} catch (err) {
			if (id === this.id) this.machinesError = err;
		}
	}

	async loadSkills() {
		const id = this.id;
		try {
			const skills = await api.laneSkills(id);
			if (id !== this.id) return;
			this.skills = skills;
			this.skillsError = null;
		} catch (err) {
			if (id === this.id) this.skillsError = err;
		}
	}

	async loadKeys() {
		const id = this.id;
		try {
			const keys = await api.keys(id);
			if (id !== this.id) return;
			this.keys = keys;
			this.keysError = null;
		} catch (err) {
			if (id === this.id) this.keysError = err;
		}
	}

	async createKey(kind: KeyKind, name: string, days?: number): Promise<CreatedKey> {
		const created = await api.createKey(this.id, kind, name, days);
		const entry = {
			...this.fresh,
			[kind]: { id: created.key.id, name: created.key.name, token: created.token }
		};
		revealed.set(this.id, entry);
		this.fresh = entry;
		this.keys = [created.key, ...(this.keys ?? [])];
		return created;
	}

	async revokeKey(key: Key) {
		await api.revokeKey(this.id, key.id);
		const fresh = this.fresh[key.kind];
		if (fresh?.id === key.id) {
			const entry = { ...this.fresh };
			delete entry[key.kind];
			revealed.set(this.id, entry);
			this.fresh = entry;
		}
		await this.loadKeys();
	}

	async rename(name: string) {
		const lane = await api.renameLane(this.id, name);
		this.lane = lane;
		app.patchLane(lane);
	}

	async renameMachine(machineId: string, name: string) {
		const res = await api.renameMachine(this.id, machineId, name);
		if (this.machines) {
			this.machines = this.machines.map((m) => (m.id === machineId ? { ...m, name: res.name } : m));
		}
		void this.loadSkills();
		return res.name;
	}

	async removeMachine(machineId: string) {
		await api.removeMachine(this.id, machineId);
		if (this.machines) this.machines = this.machines.filter((m) => m.id !== machineId);
		void this.loadSkills();
		void app.refreshLanes().catch(() => {});
	}
}

export const lane = new LaneState();
