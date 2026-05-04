const db = require('../config/database');
const crypto = require('crypto');

class Token {
  static async findByCode(code) {
    const results = await db.query('SELECT * FROM tokens WHERE code = ?', [code]);
    return results[0] || null;
  }

  static async findById(id) {
    const results = await db.query('SELECT * FROM tokens WHERE id = ?', [id]);
    return results[0] || null;
  }

  static async create(data) {
    const { label, allowed_apps, created_by } = data;
    const code = crypto.randomBytes(16).toString('hex');

    const result = await db.query(
      'INSERT INTO tokens (label, code, allowed_apps, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, NOW(), NOW())',
      [label, code, JSON.stringify(allowed_apps), created_by ?? null]
    );

    return { id: result.insertId, code, label, allowed_apps };
  }

  static async update(id, data) {
    const { label, allowed_apps } = data;
    const updates = [];
    const params = [];

    if (label !== undefined) {
      updates.push('label = ?');
      params.push(label);
    }
    if (allowed_apps !== undefined) {
      updates.push('allowed_apps = ?');
      params.push(JSON.stringify(allowed_apps));
    }

    if (updates.length === 0) return false;

    updates.push('updated_at = NOW()');
    params.push(id);

    await db.query(`UPDATE tokens SET ${updates.join(', ')} WHERE id = ?`, params);
    return true;
  }

  static async delete(id) {
    await db.query('DELETE FROM tokens WHERE id = ?', [id]);
    return true;
  }

  static async all() {
    const tokens = await db.query('SELECT * FROM tokens ORDER BY created_at DESC');
    return tokens.map(token => ({
      ...token,
      allowed_apps: (() => {
        if (Array.isArray(token.allowed_apps)) return token.allowed_apps;
        if (typeof token.allowed_apps === 'string') {
          try { return JSON.parse(token.allowed_apps); }
          catch { return [token.allowed_apps]; }
        }
        return [];
      })(),
    }));
  }
}

module.exports = Token;