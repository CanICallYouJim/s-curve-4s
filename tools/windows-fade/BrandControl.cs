using System;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Text;
using System.Windows.Forms;

public sealed class IGBrandControl : Control
{
    private readonly Bitmap sprite;
    private readonly Timer timer;
    private readonly Stopwatch clock = new Stopwatch();
    private readonly Font byFont = new Font("Segoe UI", 11, FontStyle.Regular, GraphicsUnit.Pixel);
    private readonly int frameWidth;
    private readonly int frameHeight;
    private int frame;
    private bool released;

    public IGBrandControl(string imagePath)
    {
        sprite = new Bitmap(imagePath);
        if (sprite.Width % 15 != 0 || sprite.Height % 9 != 0)
        {
            sprite.Dispose();
            throw new ArgumentException("Invalid IG animation sprite");
        }
        frameWidth = sprite.Width / 15;
        frameHeight = sprite.Height / 9;
        SetStyle(ControlStyles.UserPaint | ControlStyles.AllPaintingInWmPaint |
                 ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
        Size = new Size(112, 62);
        BackColor = Color.FromArgb(21, 35, 45);
        TabStop = false;
        timer = new Timer();
        timer.Interval = 16;
        timer.Tick += OnTick;
    }

    private void OnTick(object sender, EventArgs args)
    {
        if (clock.Elapsed.TotalMilliseconds >= 3500)
        {
            ResetAnimation();
            return;
        }
        int next = Math.Min(104, (int)(clock.Elapsed.TotalSeconds * 30));
        if (frame != next)
        {
            frame = next;
            Invalidate();
        }
    }

    private void ResetAnimation()
    {
        timer.Stop();
        clock.Reset();
        frame = 0;
        Invalidate();
    }

    protected override void OnMouseEnter(EventArgs args)
    {
        base.OnMouseEnter(args);
        frame = 0;
        clock.Restart();
        timer.Start();
        Invalidate();
    }

    protected override void OnMouseLeave(EventArgs args)
    {
        ResetAnimation();
        base.OnMouseLeave(args);
    }

    protected override void OnVisibleChanged(EventArgs args)
    {
        if (!released && !Visible && timer != null) ResetAnimation();
        base.OnVisibleChanged(args);
    }

    protected override void OnPaint(PaintEventArgs args)
    {
        base.OnPaint(args);
        Graphics graphics = args.Graphics;
        float scale = Math.Min(ClientSize.Width / 112f, ClientSize.Height / 62f);
        if (scale <= 0) return;
        graphics.ScaleTransform(scale, scale);
        graphics.InterpolationMode = InterpolationMode.HighQualityBicubic;
        graphics.PixelOffsetMode = PixelOffsetMode.HighQuality;
        graphics.TextRenderingHint = TextRenderingHint.ClearTypeGridFit;
        using (Brush text = new SolidBrush(Color.FromArgb(145, 168, 170)))
            graphics.DrawString("by", byFont, text, 0, 24);
        float height = 48f * frameHeight / frameWidth;
        RectangleF destination = new RectangleF(22, (62-height)/2, 48, height);
        RectangleF source = new RectangleF((frame % 15) * frameWidth,
                                         (frame / 15) * frameHeight,
                                         frameWidth, frameHeight);
        graphics.DrawImage(sprite, destination, source, GraphicsUnit.Pixel);
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing && !released)
        {
            released = true;
            timer.Stop();
            timer.Tick -= OnTick;
            timer.Dispose();
            sprite.Dispose();
            byFont.Dispose();
        }
        base.Dispose(disposing);
    }
}
