require('dotenv').config();
const express = require('express');
const cors = require('cors');
const pm2 = require('pm2');
const routes = require('./src/routes');
const AppResponse = require('./src/http/response/AppResponse');

const app = express();

// Middleware
app.use(cors());
app.use(express.json());
app.use(express.urlencoded({ extended: true }));

// Routes
app.use('/api', routes);

// Health check
app.get('/health', (req, res) => {
  res.json({ status: 'ok', timestamp: new Date().toISOString() });
});

// 404 handler
app.use((req, res) => {
  return AppResponse.notFound(res, 'Endpoint tidak ditemukan');
});

// Error handler
app.use((err, req, res, next) => {
  console.error(err.stack);
  return AppResponse.error(res, 'Terjadi kesalahan server');
});

// Connect to PM2 then start server
pm2.connect((err) => {
  if (err) {
    console.error('Gagal terhubung ke PM2:', err);
    process.exit(2);
  }

  const PORT = process.env.PORT || 3000;
  app.listen(PORT, () => {
    console.log(`Server berjalan di port ${PORT}`);
    console.log(`Health check: http://localhost:${PORT}/health`);
  });
});
