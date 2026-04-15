(function () {
	try {
		var es = new EventSource("/__livereload");
		es.onmessage = function (e) {
			try {
				var msg = JSON.parse(e.data);
				if (msg && msg.changes) {
					for (var i = 0; i < msg.changes.length; i++) {
						var c = msg.changes[i];
						console.log("livereload: " + c.op + " " + c.path);
					}
				}
			} catch (err) {
				/* Non-JSON payload — fall through and reload anyway. */
			}
			location.reload();
		};
		es.onerror = function () {
			/* EventSource auto-reconnects */
		};
	} catch (e) {
		console.warn("livereload: " + e);
	}
})();
