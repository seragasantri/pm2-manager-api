const mysql = require('mysql2/promise');
const bcrypt = require('bcryptjs');
require('dotenv').config();

async function seeder() {
  const connection = await mysql.createConnection({
    host: process.env.DB_HOST || 'localhost',
    user: process.env.DB_USER || 'root',
    password: process.env.DB_PASSWORD || '',
    database: process.env.DB_DATABASE || 'pm2_manager',
  });

  console.log('Seeder database dimulai...');

  // Seed super admin user
  const hashedPassword = await bcrypt.hash('superadmin123', 10);
  await connection.execute(
    `INSERT IGNORE INTO users (username, password, role) VALUES (?, ?, ?)`,
    ['superadmin', hashedPassword, 'superadmin']
  );
  console.log('[OK] Super admin user sudah dibuat (username: superadmin, password: superadmin123)');

  // Seed sample apps
  const apps = [
    { name: 'pm2-ui', description: 'PM2 Manager Frontend' },
    { name: 'pm2-manager-api', description: 'PM2 Manager Backend API' },
  ];

  for (const app of apps) {
    await connection.execute(
      `INSERT IGNORE INTO apps (name, description) VALUES (?, ?)`,
      [app.name, app.description]
    );
  }
  console.log('[OK] Sample apps sudah dibuat');

  await connection.end();
  console.log('Seeder selesai!');
}

seeder().catch((err) => {
  console.error('Seeder gagal:', err.message);
  process.exit(1);
});
