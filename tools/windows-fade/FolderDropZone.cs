using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Text;
using System.Windows.Forms;

public sealed class FolderDropZone : Control
{
    private readonly Bitmap folder;
    private readonly Font caption = new Font("Segoe UI", 18, FontStyle.Bold, GraphicsUnit.Pixel);
    private string message = "";
    private bool dragging;
    private bool selected;

    public FolderDropZone(string folderPath)
    {
        folder = new Bitmap(folderPath);
        DoubleBuffered = true;
        ResizeRedraw = true;
        TabStop = false;
        AccessibleRole = AccessibleRole.StaticText;
    }

    public void ShowState(string text, bool over, bool hasFolder)
    {
        if (message == text && dragging == over && selected == hasFolder) return;
        message = text;
        dragging = over;
        selected = hasFolder;
        AccessibleName = text;
        Invalidate();
    }

    protected override void OnPaint(PaintEventArgs args)
    {
        base.OnPaint(args);
        if (ClientSize.Width < 4 || ClientSize.Height < 4) return;

        Graphics graphics = args.Graphics;
        graphics.ScaleTransform(ClientSize.Width / 604f, ClientSize.Height / 222f);
        graphics.SmoothingMode = SmoothingMode.AntiAlias;
        graphics.InterpolationMode = InterpolationMode.HighQualityBicubic;
        graphics.TextRenderingHint = TextRenderingHint.AntiAliasGridFit;

        using (var outline = new GraphicsPath())
        using (var fill = new SolidBrush(dragging ? Color.FromArgb(35, 71, 72) : Color.FromArgb(29, 51, 60)))
        using (var border = new Pen(dragging ? Color.FromArgb(132, 231, 210) : selected ? Color.FromArgb(91, 155, 147) : Color.FromArgb(114, 149, 151), dragging ? 2f : 1.5f))
        using (var text = new SolidBrush(dragging || selected ? Color.FromArgb(164, 243, 225) : Color.FromArgb(239, 247, 247)))
        using (var align = new StringFormat { Alignment = StringAlignment.Center })
        {
            outline.AddArc(1, 1, 28, 28, 180, 90);
            outline.AddArc(575, 1, 28, 28, 270, 90);
            outline.AddArc(575, 193, 28, 28, 0, 90);
            outline.AddArc(1, 193, 28, 28, 90, 90);
            outline.CloseFigure();
            if (!dragging && !selected) border.DashStyle = DashStyle.Dash;
            graphics.FillPath(fill, outline);
            graphics.DrawPath(border, outline);
            graphics.DrawImage(folder, new Rectangle(239, 37, 126, 126));
            graphics.DrawString(message, caption, text, new RectangleF(12, 162, 580, 40), align);
        }
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            folder.Dispose();
            caption.Dispose();
        }
        base.Dispose(disposing);
    }
}
