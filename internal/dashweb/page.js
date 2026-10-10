// The page's one script. It asks for the screen again when told to and
// every five seconds, and puts in only the parts that changed; it counts the
// age line and an answer's settling time; it turns the page pale when the
// connection is down; it gives the panes' keys their meaning here; and it
// shows a notice for a new item when the window is not in front. Without it
// every button still works and the page is reloaded by hand.
'use strict';
(() => {
	const doc = document, root = doc.documentElement, parts = ['top', 'fresh', 'main', 'foot'];
	const $ = id => doc.getElementById(id);
	const last = {}, seen = {};
	let got = Date.now(), tried = got, asking = false, down = 0, live = null, fails = [], started = false, sel = '';
	try { sel = sessionStorage.getItem('sel') || ''; } catch (e) { /* no storage: the choice lasts one screen */ }
	parts.forEach(id => { if ($(id)) last[id] = $(id).outerHTML; });

	// swap puts in each part that is not what was sent last time. What was
	// typed, what was opened and where the cursor stood are carried over.
	function swap(next) {
		parts.forEach(id => {
			const old = $(id), now = next.getElementById(id);
			if (!old || !now || last[id] === now.outerHTML) return;
			last[id] = now.outerHTML;
			const at = doc.activeElement, focus = at && at.id && old.contains(at) && [at.id, at.selectionStart, at.selectionEnd];
			old.querySelectorAll('input[id]').forEach(i => {
				const n = next.getElementById(i.id);
				if (n && i.value !== i.defaultValue) n.value = i.value;
			});
			old.querySelectorAll('details[id]').forEach(d => {
				const n = next.getElementById(d.id);
				if (n && d.open) n.open = true;
			});
			old.replaceWith(doc.adoptNode(now));
			const f = focus && $(focus[0]);
			if (f) {
				f.focus();
				try { f.setSelectionRange(focus[1], focus[2]); } catch (e) { /* not a line of text */ }
			}
		});
		look();
	}

	// look is what the script does to a screen that has just arrived.
	function look() {
		choose(0);
		doc.querySelectorAll('#you > .item:not(.held)').forEach(a => {
			if (seen[a.id]) return;
			seen[a.id] = true;
			if (started && !doc.hasFocus() && window.Notification && Notification.permission === 'granted') {
				new Notification(a.dataset.title, { body: a.dataset.text, tag: a.id });
			}
		});
		const b = $('notice');
		if (b) b.hidden = !window.Notification || Notification.permission !== 'default';
		started = true;
	}

	function ask() {
		if (asking || doc.hidden) return;
		asking = true;
		tried = Date.now();
		fetch(location.href, { credentials: 'same-origin', cache: 'no-store' }).then(r => {
			if (r.redirected) return location.reload(); // signed out: the browser goes where it is sent
			if (!r.ok) throw new Error(r.status);
			return r.text().then(html => {
				got = Date.now();
				down = 0;
				root.classList.remove('off');
				swap(new DOMParser().parseFromString(html, 'text/html'));
			});
		}).catch(() => {
			// Not connected: pale, and since when. Old figures are never shown as fresh.
			if (!down) down = Date.now();
			const f = $('fresh'), d = new Date(down), two = n => String(n).padStart(2, '0');
			root.classList.add('off');
			last.fresh = '';
			if (f) f.textContent = 'not connected since ' + two(d.getHours()) + ':' + two(d.getMinutes());
		}).finally(() => { asking = false; });
	}

	// listen holds a connection open on which the server says "now". One that
	// fails twice in a minute is let go, and the asking every five seconds
	// carries the page until a minute has passed.
	function listen() {
		const t = Date.now();
		fails = fails.filter(x => t - x < 60000);
		if (live || doc.hidden || !window.EventSource || fails.length > 1) return;
		live = new EventSource('/live');
		live.onmessage = ask;
		live.onerror = () => {
			fails.push(Date.now());
			if (fails.length > 1) {
				live.close();
				live = null;
			}
		};
	}

	// A hidden tab lets go of its connection and catches up when shown.
	doc.addEventListener('visibilitychange', () => {
		if (doc.hidden && live) {
			live.close();
			live = null;
		} else if (!doc.hidden) {
			listen();
			ask();
		}
	});

	setInterval(() => {
		const f = $('fresh'), b = f && f.querySelector('b');
		if (b && !down) b.textContent = Number(f.dataset.age) + Math.floor((Date.now() - got) / 1000);
		doc.querySelectorAll('.left').forEach(l => {
			const n = Number(l.textContent) - 1;
			if (n >= 0) l.textContent = n;
			if (n === 0) ask();
		});
		listen();
		if (Date.now() - tried >= 5000) ask();
	}, 1000);

	// choose moves the choice among the items; it is an item, never a row,
	// and never moves by itself.
	function choose(by) {
		const all = Array.from(doc.querySelectorAll('#you > .item'));
		let i = all.findIndex(a => a.id === sel);
		if (by && all.length) {
			i = i < 0 ? 0 : Math.max(0, Math.min(all.length - 1, i + by));
			sel = all[i].id;
			try { sessionStorage.setItem('sel', sel); } catch (e) { /* as above */ }
			all[i].scrollIntoView({ block: 'nearest' });
		}
		all.forEach(a => a.classList.toggle('sel', a.id === sel));
	}

	doc.addEventListener('keydown', e => {
		const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
		if (typing && e.key === 'Escape') e.target.blur();
		if (typing || e.ctrlKey || e.metaKey || e.altKey || !/^[a-zA-Z0-9/]$/.test(e.key)) return;
		if (e.key === 'j' || e.key === 'k') return choose(e.key === 'j' ? 1 : -1);
		if (e.key === 'a') {
			sel = '';
			return choose(1);
		}
		const q = '[data-key="' + e.key + '"]', on = sel && $(sel);
		const b = on && on.querySelector(q) || doc.querySelector('#top ' + q + ', #foot ' + q) || e.key === 'u' && doc.querySelector('#you ' + q);
		if (!b) return;
		e.preventDefault();
		const d = b.tagName === 'SUMMARY' && b.parentNode;
		const line = b.tagName === 'INPUT' ? b : d && d.querySelector('input:not([type=hidden])');
		if (d && d.open && !line) d.querySelector('button').click(); // the second press is the "sure"
		else if (d) d.open = true;
		else if (!line) b.click();
		if (line) {
			line.focus();
			line.setSelectionRange(line.value.length, line.value.length);
		}
	});

	// "Notify me here" asks the browser's leave once. On a phone it then
	// hands the server where this phone's nudges go.
	doc.addEventListener('click', e => {
		if (e.target.id !== 'notice') return;
		Notification.requestPermission().then(leave => {
			look();
			const t = doc.querySelector('input[name=t]'), sw = navigator.serviceWorker;
			if (leave !== 'granted' || !root.classList.contains('phone') || !sw || !t) return;
			return sw.register('/a/notice.js').then(reg => new Promise(ok => {
				const w = reg.installing || reg.waiting;
				if (reg.active || !w) return ok(reg);
				w.addEventListener('statechange', () => { if (w.state === 'activated') ok(reg); });
			})).then(reg => fetch('/push', { credentials: 'same-origin' }).then(r => r.text()).then(key =>
				key.trim() && reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key.trim() })
			)).then(s => {
				if (!s) return;
				const j = s.toJSON();
				return fetch('/push', { method: 'POST', credentials: 'same-origin',
					body: new URLSearchParams({ t: t.value, endpoint: j.endpoint, p256dh: j.keys.p256dh, auth: j.keys.auth }) });
			});
		}).catch(() => { /* refused, or no push here: the page still shows everything */ });
	});

	look();
	listen();
})();
