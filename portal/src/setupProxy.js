const {createProxyMiddleware} = require('http-proxy-middleware');

const API_TARGET = process.env.PORTAL_API_TARGET || 'http://127.0.0.1:1444/';

module.exports = function (app) {
  app.use(
    '/api/',
    createProxyMiddleware({
      target: API_TARGET,
      // changeOrigin: true,
      // pathRewrite: {
      //   '^/api/': '/'
      // },
    })
  );
};
