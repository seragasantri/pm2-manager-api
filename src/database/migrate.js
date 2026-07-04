const mysql = require("mysql2/promise");
require("dotenv").config();

async function migrate() {
  const connection = await mysql.createConnection({
    host: process.env.DB_HOST || "localhost",
    user: process.env.DB_USER || "root",
    password: process.env.DB_PASSWORD || "",
    database: process.env.DB_NAME || "pm2_manager",
  });

  console.log("Migrasi database dimulai...");

  // Create users table
  await connection.execute(`
    CREATE TABLE IF NOT EXISTS users (
      id INT AUTO_INCREMENT PRIMARY KEY,
      username VARCHAR(100) NOT NULL UNIQUE,
      password VARCHAR(255) NOT NULL,
      role ENUM('superadmin', 'user') DEFAULT 'user',
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
    )
  `);
  console.log("[OK] Tabel users sudah dibuat");

  // Create apps table
  await connection.execute(`
    CREATE TABLE IF NOT EXISTS apps (
      id INT AUTO_INCREMENT PRIMARY KEY,
      name VARCHAR(100) NOT NULL UNIQUE,
      cwd VARCHAR(255) DEFAULT '',
      pm_id VARCHAR(64) DEFAULT '',
      description TEXT,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
    )
  `);
  console.log("[OK] Tabel apps sudah dibuat");

  // Alter pm_id column from INT to VARCHAR(64) if exists
  try {
    await connection.execute(`ALTER TABLE apps MODIFY COLUMN pm_id VARCHAR(64) DEFAULT ''`);
    console.log("[OK] Kolom pm_id diubah ke VARCHAR(64)");
  } catch (err) {
    console.log("[SKIP] Kolom pm_id sudah VARCHAR atau tidak ada:", err.message);
  }

  // Create tokens table
  await connection.execute(`
    CREATE TABLE IF NOT EXISTS tokens (
      id INT AUTO_INCREMENT PRIMARY KEY,
      code VARCHAR(64) NOT NULL UNIQUE,
      label VARCHAR(255) NOT NULL,
      allowed_apps JSON NOT NULL,
      created_by INT,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
      FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
    )
  `);
  console.log("[OK] Tabel tokens sudah dibuat");

  await connection.end();
  console.log("Migrasi selesai!");
}

migrate().catch((err) => {
  console.error("Migrasi gagal:", err.message);
  process.exit(1);
});
