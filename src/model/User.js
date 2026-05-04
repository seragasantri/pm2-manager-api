const db = require('../config/database');

class User {
  static async findById(id) {
    const results = await db.query('SELECT * FROM users WHERE id = ?', [id]);
    return results[0] || null;
  }

  static async findByUsername(username) {
    const results = await db.query('SELECT * FROM users WHERE username = ?', [username]);
    return results[0] || null;
  }

  static async create(data) {
    const { username, password, role } = data;
    const result = await db.query(
      'INSERT INTO users (username, password, role) VALUES (?, ?, ?)',
      [username, password, role || 'user']
    );
    return result.insertId;
  }

  static async update(id, data) {
    const { username, password } = data;
    const updates = [];
    const params = [];

    if (username) {
      updates.push('username = ?');
      params.push(username);
    }
    if (password) {
      updates.push('password = ?');
      params.push(password);
    }

    if (updates.length === 0) return false;

    params.push(id);
    await db.query(`UPDATE users SET ${updates.join(', ')} WHERE id = ?`, params);
    return true;
  }

  static async delete(id) {
    await db.query('DELETE FROM users WHERE id = ?', [id]);
    return true;
  }

  static async all() {
    return db.query('SELECT id, username, role, created_at FROM users ORDER BY created_at DESC');
  }
}

module.exports = User;
