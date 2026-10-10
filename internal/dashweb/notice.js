// The notice script of a phone. It shows the nudge the page's own server
// sent, and a tap opens the page; an address that is not the page's own is
// never opened.
'use strict';
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('push', e => {
	let n = {};
	try { n = e.data.json(); } catch (err) { n = { title: e.data ? e.data.text() : '' }; }
	e.waitUntil(self.registration.showNotification(n.title || 'WhaleShark',
		{ body: n.body || '', tag: n.tag || '', data: n.link || '/' }));
});
self.addEventListener('notificationclick', e => {
	const to = new URL(e.notification.data, self.location.origin);
	e.notification.close();
	e.waitUntil(self.clients.openWindow(to.origin === self.location.origin ? to.href : '/'));
});
