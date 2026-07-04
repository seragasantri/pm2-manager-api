require("dotenv").config();
const express = require("express");
const cors = require("cors");
const routes = require("./src/routes");
const AppResponse = require("./src/http/response/AppResponse");

const app = express();

// Middleware
app.use(cors());
app.use(express.json());
app.use(express.urlencoded({ extended: true }));

// ==============================================================
// SOLUSI UNTUK NGINX REVERSE PROXY
// Kita membuat satu router utama yang akan menampung semua routes
// ==============================================================
const mainRouter = express.Router();

// Routes utama aplikasi Anda (dimasukkan ke dalam mainRouter)
mainRouter.use("/api", routes);

// Health check
mainRouter.get("/health", (req, res) => {
  res.json({ status: "ok", timestamp: new Date().toISOString() });
});

// Daftarkan mainRouter ke root path (jika Nginx berhasil memotong path)
app.use("/", mainRouter);

// Daftarkan mainRouter ke path proxy (jika Nginx TIDAK memotong path)
// Ini adalah KUNCI agar tidak terjadi 404 saat diakses via Nginx
app.use("/panelPm/backend", mainRouter);

// 404 handler (diletakkan setelah semua routing)
app.use((req, res) => {
  return AppResponse.notFound(res, "Endpoint tidak ditemukan");
});

// Error handler
app.use((err, req, res, next) => {
  console.error(err.stack);
  return AppResponse.error(res, "Terjadi kesalahan server");
});

// Start server langsung (tidak perlu PM2 connect lagi)
const PORT = process.env.PORT || 3003;
app.listen(PORT, () => {
  console.log(`Server berjalan di port ${PORT}`);
  console.log(`Health check (Lokal): http://localhost:${PORT}/health`);
  console.log(
    `Health check (Public): https://sim-obe.radenfatah.ac.id/panelPm/backend/health`,
  );
});
