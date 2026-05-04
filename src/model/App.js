const db = require('../config/database');

class App {
  static async findByName(name) {
    const results = await db.query('SELECT * FROM apps WHERE name = ?', [name]);
    return results[0] || null;
  }

  static async all() {
    return db.query('SELECT * FROM apps ORDER BY name ASC');
  }

  static async create(data) {
    const { name, cwd, pm_id } = data;
    const result = await db.query(
      'INSERT INTO apps (name, cwd, pm_id, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())',
      [name, cwd, pm_id]
    );
    return result.insertId;
  }

  static async syncFromPM2(pm2Apps) {
    const connection = await db.getPool().getConnection();
    await connection.beginTransaction();

    try {
      // Get existing apps from DB
      const [existingApps] = await connection.execute('SELECT name, id FROM apps');
      const existingNames = existingApps.map(a => a.name);
      const pm2Names = pm2Apps.map(a => a.name);

      // Add new apps
      for (const app of pm2Apps) {
        if (!existingNames.includes(app.name)) {
          await connection.execute(
            'INSERT INTO apps (name, cwd, pm_id, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())',
            [app.name, app.cwd || '', app.pm_id || 0]
          );
        }
      }

      // Optionally remove apps not in PM2 anymore
      for (const existing of existingApps) {
        if (!pm2Names.includes(existing.name)) {
          await connection.execute('DELETE FROM apps WHERE id = ?', [existing.id]);
        }
      }

      await connection.commit();
    } catch (error) {
      await connection.rollback();
      throw error;
    } finally {
      connection.release();
    }
  }

  static async getAllowedApps(userRole, allowedApps = []) {
    if (userRole === 'superadmin') {
      return this.all();
    }
    // Filter by allowed apps for regular users
    if (allowedApps.length === 0) return [];
    const placeholders = allowedApps.map(() => '?').join(',');
    return db.query(`SELECT * FROM apps WHERE name IN (${placeholders}) ORDER BY name ASC`, allowedApps);
  }
}

module.exports = App;