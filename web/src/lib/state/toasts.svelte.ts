export type ToastKind = 'success' | 'error' | 'info';

export interface Toast {
	id: number;
	kind: ToastKind;
	message: string;
}

let nextId = 1;

class Toasts {
	items = $state<Toast[]>([]);

	show(message: string, kind: ToastKind = 'success', ms = 3200) {
		const id = nextId++;
		this.items.push({ id, kind, message });
		if (this.items.length > 4) this.items.shift();
		setTimeout(() => this.dismiss(id), ms);
	}

	error(message: string) {
		this.show(message, 'error', 5200);
	}

	dismiss(id: number) {
		const i = this.items.findIndex((t) => t.id === id);
		if (i !== -1) this.items.splice(i, 1);
	}
}

export const toasts = new Toasts();
