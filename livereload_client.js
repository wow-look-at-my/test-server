(function () {
	try {
		var es = new EventSource("/__livereload");
		es.onmessage = function () {
			location.reload();
		};
		es.onerror = function () {
			/* EventSource auto-reconnects */
		};
	} catch (e) {
		console.warn("livereload: " + e);
	}
})();
