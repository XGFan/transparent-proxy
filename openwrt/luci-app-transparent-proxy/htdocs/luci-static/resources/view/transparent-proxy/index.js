'use strict';
'require view';

return view.extend({
	handleSaveApply: null,
	handleSave: null,
	handleReset: null,

	render: function() {
		var host = (window.location && window.location.hostname) ? window.location.hostname : '127.0.0.1';
		var fallbackUrl = '//' + host + ':1444/';

		return E('div', { 'class': 'cbi-map' }, [
			E('h2', _('Transparent Proxy')),
			E('p', {
				'class': 'alert-message notice',
				'data-task9-fallback': 'TASK9_FALLBACK_BRANCH_REQUIRED'
			}, _('当前镜像未启用同源承载，已降级为独立管理页。')),
			E('p', _('请通过以下地址访问独立管理页：')),
			E('p', [
				E('a', {
					href: fallbackUrl,
					target: '_blank',
					rel: 'noopener noreferrer',
					'data-testid': 'tp-fallback-link'
				}, fallbackUrl)
			]),
			E('p', {
				'class': 'cbi-value-description',
				'data-same-origin-supported': '0'
			}, _('固定降级端口: :1444')),
			E('script', {
				type: 'text/javascript'
			}, 'window.__TP_LUCI_SAME_ORIGIN_SUPPORTED__=0;window.__TP_LUCI_FALLBACK_URL__=' + JSON.stringify(fallbackUrl) + ';')
		]);
	}
});
