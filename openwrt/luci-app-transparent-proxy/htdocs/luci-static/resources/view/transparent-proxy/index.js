'use strict';
'require view';

return view.extend({
	handleSaveApply: null,
	handleSave: null,
	handleReset: null,

	render: function() {
		var iframeSrc = '//' + window.location.host + ':1444/';

		return E('div', { 'class': 'cbi-map' }, [
			E('iframe', {
				src: iframeSrc,
				style: 'width: 100%; height: calc(100vh - 120px); border: none; border-radius: 4px;',
				allowtransparency: 'true'
			})
		]);
	}
});
