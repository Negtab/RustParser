struct Point {
    x: f64,
    y: f64,
}

impl Point {
    fn new(x: f64, y: f64) -> Self {
        Point { x, y }
    }

    fn distance(&self, other: &Point) -> f64 {
        let dx = self.x - other.x;
        let dy = self.y - other.y;
        (dx * dx + dy * dy).sqrt()
    }
}

enum Shape {
    Circle(f64),
    Rectangle { width: f64, height: f64 },
}

impl Shape {
    fn area(&self) -> f64 {
        match self {
            Shape::Circle(r) => std::f64::consts::PI * (r * r),
            Shape::Rectangle { width, height } => width * height,
        }
    }
}

trait Describe {
    fn describe(&self) -> String;
}

impl Describe for Shape {
    fn describe(&self) -> String {
        match self {
            Shape::Circle(r) => format!("Circle with radius {}", r),
            Shape::Rectangle { width, height } => format!("Rectangle {}x{}", width, height),
        }
    }
}

fn factorial(n: u64) -> u64 {
    if n <= 1 {
        1
    } else {
        n * factorial(n - 1)
    }
}

fn classify(n: i32) -> &'static str {
    if n < 0 {
        "negative"
    } else if n == 0 {
        "zero"
    } else {
        "positive"
    }
}

fn largest<T: PartialOrd + Copy>(items: &[T]) -> T {
    let mut max = items[0];
    for &item in items.iter() {
        if item > max {
            if max < item {
                max = item;
                break;
            }
        }
    }
    max
}

fn safe_divide(a: f64, b: f64) -> Result<f64, String> {
    if b == 0.0 {
        Err("division by zero".to_string())
    } else {
        Ok(a / b)
    }
}
fn main() {
    let a = Point::new(0.0, 0.0);
    let b = Point::new(3.0, 4.0);
    println!("Distance: {}", a.distance(&b));

    let mut sum = 0;
    for i in 1..=10 {
        sum += i;
    }
    println!("Sum: {}", sum);

    let values = vec![5, 3, 8, 1, 9];
    println!("Max: {}", largest(&values));

    println!("5! = {}", factorial(5));
    println!("{}", classify(-3));

    let shapes = vec![
        Shape::Circle(2.0),
        Shape::Rectangle { width: 3.0, height: 4.0 },
    ];
    for shape in &shapes {
        println!("{} has area {:.2}", shape.describe(), shape.area());
    }

    match safe_divide(10.0, 2.0) {
        Ok(result) => println!("10 / 2 = {}", result),
        Err(e) => println!("Error: {}", e),
    }
    match safe_divide(10.0, 0.0) {
        Ok(result) => println!("10 / 0 = {}", result),
        Err(e) => println!("Error: {}", e),
    }

    let doubled: Vec<i32> = values.iter().map(|x| x * 2).collect();
    println!("Doubled: {:?}", doubled);
}
