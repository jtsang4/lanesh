import { api } from '../api';
import type { Info, Lane } from '../types';

/** Instance-wide data shared by the shell: lanes for the sidebar and server info. */
class AppState {
	lanes = $state.raw<Lane[] | null>(null);
	lanesError = $state.raw<unknown>(null);
	info = $state.raw<Info | null>(null);

	/** Base URL for snippets: the configured public URL, else this browser's origin. */
	baseUrl = $derived(
		this.info?.public_url_configured ? this.info.public_url.replace(/\/+$/, '') : location.origin
	);
	publicUrlMissing = $derived(this.info !== null && !this.info.public_url_configured);

	async refreshLanes() {
		try {
			this.lanes = await api.lanes();
			this.lanesError = null;
		} catch (err) {
			this.lanesError = err;
			throw err;
		}
	}

	async refreshInfo() {
		this.info = await api.info();
	}

	/** Updates one lane in place after a rename without refetching. */
	patchLane(lane: Lane) {
		if (!this.lanes) return;
		this.lanes = this.lanes.map((l) => (l.id === lane.id ? { ...l, name: lane.name } : l));
	}

	reset() {
		this.lanes = null;
		this.info = null;
		this.lanesError = null;
	}
}

export const app = new AppState();
