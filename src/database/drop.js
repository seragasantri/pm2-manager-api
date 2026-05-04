const mysql = require("mysql2/promise");
require("dotenv").config();

const TABLES = ["tokens", "apps", "users"];

async function drop() {
  const connection = await mysql.createConnection({
    host: process.env.DB_HOST || "localhost",
    user: process.env.DB_USER || "root",
    password: process.env.DB_PASSWORD || "",
    database: process.env.DB_NAME || "pm2_manager",
  });

  console.log("Dropping all tables...");

  await connection.execute("SET FOREIGN_KEY_CHECKS = 0");
  for (const table of TABLES) {
    await connection.execute(`DROP TABLE IF EXISTS \`${table}\``);
    console.log(`[OK] Dropped: ${table}`);
  }
  await connection.execute("SET FOREIGN_KEY_CHECKS = 1");

  await connection.end();
  console.log("Done!");
}

drop().catch((err) => {
  console.error("Drop failed:", err.message);
  process.exit(1);
});
