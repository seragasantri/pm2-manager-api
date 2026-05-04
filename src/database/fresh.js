const mysql = require("mysql2/promise");
require("dotenv").config();

const TABLES = ["tokens", "apps", "users"];

async function fresh(seed = false) {
  const connection = await mysql.createConnection({
    host: process.env.DB_HOST || "localhost",
    user: process.env.DB_USER || "root",
    password: process.env.DB_PASSWORD || "",
    database: process.env.DB_NAME || "pm2_manager",
  });

  console.log("Dropping all tables...");

  // Disable foreign key checks temporarily
  await connection.execute("SET FOREIGN_KEY_CHECKS = 0");

  for (const table of TABLES) {
    try {
      await connection.execute(`DROP TABLE IF EXISTS \`${table}\``);
      console.log(`[OK] Dropped: ${table}`);
    } catch (err) {
      console.log(`[SKIP] ${table}: ${err.message}`);
    }
  }

  await connection.execute("SET FOREIGN_KEY_CHECKS = 1");

  console.log("\nRunning migrations...");

  // users
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

  // apps
  await connection.execute(`
    CREATE TABLE IF NOT EXISTS apps (
      id INT AUTO_INCREMENT PRIMARY KEY,
      name VARCHAR(100) NOT NULL UNIQUE,
      cwd VARCHAR(255) DEFAULT '',
      pm_id INT DEFAULT 0,
      description TEXT,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
    )
  `);
  console.log("[OK] Tabel apps sudah dibuat");

  // tokens
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

  if (seed) {
    console.log("\nRunning seeders...");
    const bcrypt = require("bcryptjs");
    const conn2 = await mysql.createConnection({
      host: process.env.DB_HOST || "localhost",
      user: process.env.DB_USER || "root",
      password: process.env.DB_PASSWORD || "",
      database: process.env.DB_NAME || "pm2_manager",
    });

    // Super admin
    const hashedPassword = await bcrypt.hash(
      "M16~dvdy2hR|vB+G]Z1g-cjGK%$V':$=+eUWxT9%u}6r<z}Y)",
      10,
    );
    await conn2.execute(
      `INSERT IGNORE INTO users (username, password, role) VALUES (?, ?, ?)`,
      ["superadmin", hashedPassword, "superadmin"],
    );
    console.log("[OK] Super admin user seeded");

    // Sample apps
    const apps = [
      { name: "pm2-ui", description: "PM2 Manager Frontend" },
      { name: "pm2-manager-api", description: "PM2 Manager Backend API" },
    ];
    for (const app of apps) {
      await conn2.execute(
        `INSERT IGNORE INTO apps (name, description) VALUES (?, ?)`,
        [app.name, app.description],
      );
    }
    console.log("[OK] Sample apps seeded");

    await conn2.end();
  }

  console.log(
    seed ? "\nMigrate:fresh --seed selesai!" : "\nMigrate:fresh selesai!",
  );
}

const shouldSeed = process.argv.includes("--seed");
fresh(shouldSeed).catch((err) => {
  console.error("Migrasi gagal:", err.message);
  process.exit(1);
});
