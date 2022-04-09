const {createProxyMiddleware} = require('http-proxy-middleware');

module.exports = function (app) {
  app.use(
    '/api/',
    createProxyMiddleware({
      target: 'http://192.168.2.1:1444/',
      changeOrigin: true,
      pathRewrite: {
        '^/api/': '/'
      },
    })
  );
};